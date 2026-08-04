<script setup>
import { ref, computed, watch, onMounted } from 'vue'
import { api, jsonBody } from '../api'
import { sortResolutions } from '../utils/format'
import Icon from './Icon.vue'
import SelectMenu from './SelectMenu.vue'

const props = defineProps({
  // null → add mode; an object (managed-model record) → edit mode
  model: { type: Object, default: null },
})
const emit = defineEmits(['close', 'saved'])

const isEdit = computed(() => !!props.model)

const REF_MODE_LABEL = { none: '无', frame: '首帧/首尾帧', asset: '参考图模式' }

const catalog = ref([])
const loading = ref(true)
const selectedId = ref(props.model?.id || '')
const alias = ref(props.model?.alias || '')
const imagePrices = ref({})        // 普通价 { '1K': '', '2K': '', ... } keyed by resolutions
const videoPrices = ref({})        // 普通价 { '5s': '', '10s': '', ... } keyed by durations
const imagePricesAgent = ref({})   // 代理价(留空 = 跟随普通价)
const videoPricesAgent = ref({})   // 代理价(留空 = 跟随普通价)
const textPrice = ref('')          // 文本模型固定每请求价格
const textPriceAgent = ref('')     // 文本模型代理每请求价格
// Display weight — admin-set (NOT a catalog param): higher = higher up the
// dropdown / model list. Defaults to the stored value in edit mode, else 0.
const weight = ref(Number(props.model?.weight) || 0)
const error = ref('')
const saving = ref(false)

// The entry whose params drive the form: in edit mode it's the stored record,
// in add mode it's the catalog row for the picked id. All generation params are
// read straight off it — the admin never types them, only the price.
const entry = computed(() => {
  if (isEdit.value) return props.model
  return catalog.value.find((e) => e.id === selectedId.value) || null
})
const isVideo = computed(() => entry.value?.type === 'video')
const isText = computed(() => entry.value?.type === 'text')
// Display tiers in canonical ascending order (720p before 1080p; 1K<2K<4K)
// regardless of how the catalog/stored record happens to list them.
const resolutions = computed(() => sortResolutions(entry.value?.resolutions || []))
// Duration tiers for the price inputs. Prefer the declared `durations`; fall
// back to the keys of any stored duration_prices so a model saved without a
// `durations` array (the legacy sync bug) is still viewable/editable.
const durationTiers = computed(() => {
  const e = entry.value
  if (!e) return []
  const ds = e.durations || []
  return ds.length ? ds : Object.keys(e.duration_prices || {})
})

// Dropdown options: catalog rows not already in the managed store.
const addOptions = computed(() =>
  catalog.value
    .filter((e) => !e.added)
    .map((e) => ({ value: e.id, label: `${e.id}　·　${e.type === 'text' ? '文本' : (e.type === 'video' ? '视频' : '图像')}` }))
)

function resetPrices(e) {
  imagePrices.value = {}
  videoPrices.value = {}
  imagePricesAgent.value = {}
  videoPricesAgent.value = {}
  textPrice.value = ''
  textPriceAgent.value = ''
  if (!e) return
  // Both image and video price per resolution; video ALSO prices per duration
  // (real video price = resolution price + duration price).
  for (const r of (e.resolutions || [])) { imagePrices.value[r] = ''; imagePricesAgent.value[r] = '' }
  if (e.type === 'video') {
    for (const d of (e.durations || [])) { videoPrices.value[d] = ''; videoPricesAgent.value[d] = '' }
  }
}

// In add mode, switching the selected model rebuilds the price inputs to match
// that model's resolution / duration tiers.
watch(selectedId, () => { if (!isEdit.value) resetPrices(entry.value) })

onMounted(async () => {
  const r = await api('/catalog')
  catalog.value = r.data?.data || []
  loading.value = false
  if (isEdit.value) {
    const m = props.model
    if (m.type === 'text') {
      textPrice.value = m.prices?.request ?? ''
      textPriceAgent.value = m.prices_agent?.request ?? ''
    }
    // Resolution prices apply to both; video additionally has duration prices.
    // Agent prices are an optional overlay (blank = follows the normal price).
    for (const r of (m.resolutions || [])) {
      imagePrices.value[r] = m.prices?.[r] ?? ''
      imagePricesAgent.value[r] = m.prices_agent?.[r] ?? ''
    }
    if (m.type === 'video') {
      const durs = (m.durations && m.durations.length) ? m.durations : Object.keys(m.duration_prices || {})
      for (const d of durs) {
        videoPrices.value[d] = m.duration_prices?.[d] ?? ''
        videoPricesAgent.value[d] = m.duration_prices_agent?.[d] ?? ''
      }
    }
  }
})

async function save() {
  const e = entry.value
  if (!e) { error.value = '请选择模型'; return }
  error.value = ''

  // Collect valid (>=0) numeric prices from a {key: value} ref into a plain map.
  const collect = (src, keys) => {
    const out = {}
    for (const k of keys) {
      const raw = String(src[k] ?? '').trim()
      if (raw === '') continue
      const n = Number(raw)
      if (!isNaN(n) && n >= 0) out[k] = n
    }
    return out
  }

  let payload
  if (e.type === 'text') {
    const normal = Number(textPrice.value)
    if (String(textPrice.value).trim() === '' || Number.isNaN(normal) || normal < 0) {
      error.value = '请填写有效的每次请求价格'; return
    }
    const agentRaw = String(textPriceAgent.value).trim()
    const prices_agent = {}
    if (agentRaw !== '') {
      const agent = Number(agentRaw)
      if (Number.isNaN(agent) || agent < 0) { error.value = '代理价不能小于 0'; return }
      prices_agent.request = agent
    }
    payload = {
      type: 'text', provider: e.provider, alias: alias.value.trim(),
      prices: { request: normal }, prices_agent,
      ratios: [], resolutions: [], durations: [], duration_prices: {}, duration_prices_agent: {},
      max_reference_images: 0, max_reference_videos: 0, max_reference_audios: 0, max_reference_media: 0,
      supports_audio_output: false, reference_mode: 'none', image_to_image: false,
      weight: Number(weight.value) || 0,
    }
  } else if (e.type === 'video') {
    // Real video price = resolution price + duration price. Both tiers are
    // priced independently; a blank tier on either axis = unsupported. Charge
    // happens server-side as prices[res] + duration_prices[dur].
    const prices = collect(imagePrices.value, e.resolutions || [])
    const duration_prices = collect(videoPrices.value, durationTiers.value)
    // 代理价:可选覆盖,留空的档跟随普通价。
    const prices_agent = collect(imagePricesAgent.value, e.resolutions || [])
    const duration_prices_agent = collect(videoPricesAgent.value, durationTiers.value)
    if (!Object.keys(prices).length) { error.value = '至少填写一个分辨率价格'; return }
    if (!Object.keys(duration_prices).length) { error.value = '至少填写一个时长价格'; return }
    payload = {
      type: 'video',
      provider: e.provider,
      ratios: e.ratios || [],
      resolutions: e.resolutions || [],
      prices,
      // durations MUST track duration_prices — the model list / docs iterate
      // `durations` to render the per-second price chips. Persisting prices
      // without the matching durations array hides them. Keep them in sync.
      durations: Object.keys(duration_prices),
      duration_prices,
      prices_agent,
      duration_prices_agent,
      alias: alias.value.trim(),
      max_reference_images: e.max_reference_images || 0,
      max_reference_videos: e.max_reference_videos || 0,
      max_reference_audios: e.max_reference_audios || 0,
      max_reference_media: e.max_reference_media || 0,
      supports_audio_output: !!e.supports_audio_output,
      reference_mode: e.reference_mode || 'none',
      weight: Number(weight.value) || 0,
    }
  } else {
    const prices = collect(imagePrices.value, e.resolutions || [])
    const prices_agent = collect(imagePricesAgent.value, e.resolutions || [])
    if (!Object.keys(prices).length) { error.value = '至少填写一个画质价格'; return }
    payload = {
      type: 'image',
      provider: e.provider,
      ratios: e.ratios || [],
      prices,
      prices_agent,
      image_to_image: !!e.image_to_image,
      alias: alias.value.trim(),
      // 多参考图:把目录定义的张数(gpt=3/seedream=6/flux=4 …)写进模型,
      // 否则后端仍按旧值(默认 1)限制。
      max_reference_images: e.max_reference_images || 0,
      max_reference_videos: e.max_reference_videos || 0,
      max_reference_audios: e.max_reference_audios || 0,
      max_reference_media: e.max_reference_media || 0,
      supports_audio_output: !!e.supports_audio_output,
      reference_mode: e.reference_mode || 'none',
      weight: Number(weight.value) || 0,
    }
  }

  saving.value = true
  const r = isEdit.value
    ? await api(`/managed-models/${encodeURIComponent(e.id)}`, jsonBody('PATCH', payload))
    : await api('/managed-models', jsonBody('POST', { id: e.id, ...payload }))
  saving.value = false
  if (r.ok) emit('saved')
  else error.value = r.data?.detail || `保存失败 (${r.status})`
}
</script>

<template>
  <div class="fixed inset-0 z-50 bg-black/70 backdrop-blur-sm flex items-start justify-center overflow-y-auto p-4"
       @click.self="emit('close')">
    <div class="card !shadow-2xl my-12 w-full max-w-md">
      <div class="px-5 py-4 border-b border-white/[0.06] flex items-center justify-between">
        <h2 class="text-sm font-semibold">{{ isEdit ? '编辑价格' : '新增模型' }}</h2>
        <button @click="emit('close')" class="text-white/40 hover:text-white transition-colors">
          <Icon name="close" class="w-5 h-5" />
        </button>
      </div>

      <div class="p-5 space-y-4">
        <div v-if="loading" class="text-center text-sm text-white/40 py-8">加载支持的模型…</div>

        <template v-else>
          <!-- model id: dropdown when adding, fixed label when editing -->
          <div>
            <label class="lbl">模型</label>
            <SelectMenu v-if="!isEdit" v-model="selectedId" :options="addOptions"
                        placeholder="选择一个支持的模型" />
            <div v-else class="field font-mono !cursor-default opacity-90">{{ entry?.id }}</div>
            <p v-if="!isEdit && !addOptions.length" class="text-[11px] text-amber-300/80 mt-1.5">
              所有支持的模型都已添加。
            </p>
          </div>

          <div>
            <label class="lbl">别名</label>
            <input v-model="alias" class="field font-mono" placeholder="可选，对外名" />
            <p class="text-[11px] text-white/40 mt-1.5">设置后原模型名将不可调用,画图台 / API / 文档都改用别名</p>
          </div>

          <!-- read-only param summary, straight from the loaded catalog -->
          <div v-if="entry" class="rounded-xl bg-white/[0.03] ring-1 ring-white/[0.06] p-3.5 space-y-2.5">
            <div class="flex items-center gap-2">
              <span class="inline-flex items-center gap-1 rounded-full px-2.5 py-1 text-[11px] font-medium ring-1"
                    :class="isText ? 'bg-sky-500/10 text-sky-300 ring-sky-400/30'
                                    : (isVideo ? 'bg-fuchsia-500/10 text-fuchsia-300 ring-fuchsia-400/30'
                                               : 'bg-indigo-500/10 text-indigo-300 ring-indigo-400/30')">
                {{ isText ? '文本' : (isVideo ? '生视频' : '生图') }}
              </span>
              <span class="text-[11px] text-white/45 capitalize">{{ entry.provider }}</span>
              <span class="ml-auto text-[10px] text-white/30">参数自动加载,不可改</span>
            </div>

            <div class="grid grid-cols-[3.5rem_1fr] gap-x-3 gap-y-1.5 text-[11px]">
              <span v-if="!isText" class="text-white/40">比例</span>
              <div v-if="!isText" class="flex flex-wrap gap-1">
                <span v-for="r in (entry.ratios || [])" :key="r" class="ro-chip">{{ r }}</span>
                <span v-if="!(entry.ratios || []).length" class="text-white/30">—</span>
              </div>

              <template v-if="isText">
                <span class="text-white/40">接口</span>
                <div class="text-white/70 font-mono">/v1/chat/completions</div>
              </template>

              <template v-else-if="isVideo">
                <span class="text-white/40">分辨率</span>
                <div class="flex flex-wrap gap-1">
                  <span v-for="r in resolutions" :key="r" class="ro-chip">{{ r }}</span>
                  <span v-if="!resolutions.length" class="text-white/30">—</span>
                </div>
                <span class="text-white/40">时长</span>
                <div class="flex flex-wrap gap-1">
                  <span v-for="d in (entry.durations || [])" :key="d" class="ro-chip">{{ d }}</span>
                  <span v-if="!(entry.durations || []).length" class="text-white/30">—</span>
                </div>
                <span class="text-white/40">参考图</span>
                <div class="text-white/70">
                  {{ entry.max_reference_images > 0
                      ? `${entry.max_reference_images} 张 · ${REF_MODE_LABEL[entry.reference_mode] || entry.reference_mode}`
                      : '不支持' }}
                </div>
                <span class="text-white/40">参考视频</span>
                <div class="text-white/70">{{ entry.max_reference_videos > 0 ? `${entry.max_reference_videos} 个` : '不支持' }}</div>
                <span class="text-white/40">参考音频</span>
                <div class="text-white/70">{{ entry.max_reference_audios > 0 ? `${entry.max_reference_audios} 个` : '不支持' }}</div>
                <span class="text-white/40">素材合计</span>
                <div class="text-white/70">{{ entry.max_reference_media > 0 ? `${entry.max_reference_media} 个` : '分别计数' }}</div>
                <span class="text-white/40">同步音频</span>
                <div class="text-white/70">{{ entry.supports_audio_output ? '支持' : '不支持' }}</div>
              </template>

              <template v-else>
                <span class="text-white/40">画质</span>
                <div class="flex flex-wrap gap-1">
                  <span v-for="r in resolutions" :key="r" class="ro-chip">{{ r }}</span>
                  <span v-if="!resolutions.length" class="text-white/30">—</span>
                </div>
                <span class="text-white/40">参考图</span>
                <div class="text-white/70">{{ entry.max_reference_images > 0 ? `${entry.max_reference_images} 张` : (entry.image_to_image ? '支持' : '不支持') }}</div>
              </template>
            </div>
          </div>

          <!-- ===== PRICE: image = per-quality; video = per-quality + per-duration (additive) ===== -->
          <template v-if="entry">
            <div class="space-y-4">
              <div v-if="isText">
                <label class="lbl">每次请求价格 <span class="text-white/35">(流式与非流式相同;代理价留空 = 跟随普通价)</span></label>
                <div class="flex items-center gap-2">
                  <div class="relative flex-1">
                    <input v-model="textPrice" type="number" min="0" step="0.01" class="field !pr-10" placeholder="普通价" />
                    <span class="absolute right-2.5 top-1/2 -translate-y-1/2 text-white/30 text-[10px]">积分</span>
                  </div>
                  <div class="relative flex-1">
                    <input v-model="textPriceAgent" type="number" min="0" step="0.01" class="field !pr-10" placeholder="跟随普通" />
                    <span class="absolute right-2.5 top-1/2 -translate-y-1/2 text-amber-300/40 text-[10px]">代理</span>
                  </div>
                </div>
              </div>

              <!-- resolution prices (both image & video): 普通价 + 代理价 -->
              <div v-else>
                <label class="lbl">{{ isVideo ? '分辨率价格' : '画质价格' }} <span class="text-white/35">(普通价留空 = 不支持该档;代理价留空 = 跟随普通价)</span></label>
                <div class="space-y-2">
                  <div v-if="resolutions.length" class="flex items-center gap-2 text-[10px] text-white/35 pl-14">
                    <span class="flex-1">普通价</span>
                    <span class="flex-1">代理价</span>
                  </div>
                  <div v-for="r in resolutions" :key="r" class="flex items-center gap-2">
                    <div class="w-12 shrink-0 text-sm text-white/85 font-mono">{{ r }}</div>
                    <div class="relative flex-1">
                      <input v-model="imagePrices[r]" type="number" min="0" step="1" class="field !pr-10" placeholder="普通价" />
                      <span class="absolute right-2.5 top-1/2 -translate-y-1/2 text-white/30 text-[10px]">积分</span>
                    </div>
                    <div class="relative flex-1">
                      <input v-model="imagePricesAgent[r]" type="number" min="0" step="1" class="field !pr-10" placeholder="跟随普通" />
                      <span class="absolute right-2.5 top-1/2 -translate-y-1/2 text-amber-300/40 text-[10px]">代理</span>
                    </div>
                  </div>
                  <p v-if="!resolutions.length" class="text-xs text-white/35">该模型未声明分辨率档位</p>
                </div>
              </div>

              <!-- duration prices (video only): 普通价 + 代理价 -->
              <div v-if="isVideo">
                <label class="lbl">时长价格 <span class="text-white/35">(代理价留空 = 跟随普通价)</span></label>
                <div v-if="durationTiers.length" class="space-y-2">
                  <div class="flex items-center gap-2 text-[10px] text-white/35 pl-14">
                    <span class="flex-1">普通价</span>
                    <span class="flex-1">代理价</span>
                  </div>
                  <div v-for="d in durationTiers" :key="d" class="flex items-center gap-2">
                    <div class="w-12 shrink-0 text-sm text-white/85 font-mono">{{ d }}</div>
                    <div class="relative flex-1">
                      <input v-model="videoPrices[d]" type="number" min="0" step="1" class="field !pr-10" placeholder="普通价" />
                      <span class="absolute right-2.5 top-1/2 -translate-y-1/2 text-white/30 text-[10px]">积分</span>
                    </div>
                    <div class="relative flex-1">
                      <input v-model="videoPricesAgent[d]" type="number" min="0" step="1" class="field !pr-10" placeholder="跟随普通" />
                      <span class="absolute right-2.5 top-1/2 -translate-y-1/2 text-amber-300/40 text-[10px]">代理</span>
                    </div>
                  </div>
                </div>
                <p v-else class="text-xs text-white/35">该模型未声明时长档位</p>
              </div>

              <p v-if="isVideo" class="text-[11px] text-white/40">实付 = 分辨率价 + 时长价(例:720p 50 + 5s 30 = 80 积分)</p>

              <!-- display weight: admin-set ordering (not a catalog param) -->
              <div>
                <label class="lbl">展示权重 <span class="text-white/35">(数值越大,在下拉 / 列表中越靠前;相同权重按新建时间)</span></label>
                <input v-model="weight" type="number" step="1" class="field" placeholder="0" />
              </div>
            </div>
          </template>

          <p v-if="error" class="text-xs text-rose-300">{{ error }}</p>

          <div class="flex justify-end gap-2 pt-1">
            <button @click="emit('close')" class="btn-soft">取消</button>
            <button @click="save" :disabled="saving || !entry" class="btn-primary">{{ saving ? '保存中…' : '保存' }}</button>
          </div>
        </template>
      </div>
    </div>
  </div>
</template>

<style scoped>
.lbl {
  display: block;
  font-size: 0.72rem;
  font-weight: 500;
  color: rgb(255 255 255 / 0.55);
  margin-bottom: 0.4rem;
}

/* read-only param chip */
.ro-chip {
  display: inline-flex;
  align-items: center;
  padding: 0.1rem 0.45rem;
  font-size: 0.68rem;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  border-radius: 0.4rem;
  color: rgb(255 255 255 / 0.7);
  background: rgb(255 255 255 / 0.05);
  box-shadow: inset 0 0 0 1px rgb(255 255 255 / 0.08);
}
</style>
