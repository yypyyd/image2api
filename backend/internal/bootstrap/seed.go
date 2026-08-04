package bootstrap

import (
	"context"

	"backend/internal/model"
	"gorm.io/gorm"
)

func seedDefaults(ctx context.Context, db *gorm.DB) error {
	defaults := []model.SiteSetting{
		{Key: "site.title", Value: "Vivid"},
		{Key: "site.logo", Value: ""},
		{Key: "site.subtitle", Value: ""},
		{Key: "contact.qq", Value: "1114639355"},
		{Key: "contact.qq_link", Value: "https://qm.qq.com/q/ItgCcNA7ac"},
		{Key: "contact.qq_group", Value: "1106849765"},
		{Key: "contact.qq_group_link", Value: "https://qm.qq.com/q/976LeMFoHu"},
		{Key: "contact.email", Value: "vividairun@gmail.com"},
		{Key: "contact.shop", Value: "https://pay.ldxp.cn/shop/chiyi"},
		{Key: "auth.open", Value: "true"},
		{Key: "auth.email_code", Value: "false"},
		{Key: "auth.allow_password_reset", Value: "false"},
		{Key: "auth.allowed_email_domains", Value: ""},
		{Key: "auth.code_ttl_seconds", Value: "600"},
		{Key: "smtp.host", Value: ""},
		{Key: "smtp.port", Value: "587"},
		{Key: "smtp.username", Value: ""},
		{Key: "smtp.password", Value: ""},
		{Key: "smtp.from_addr", Value: ""},
		{Key: "smtp.use_tls", Value: "true"},
		{Key: "proxy.url", Value: ""},
		{Key: "credits.checkin_enabled", Value: "true"},
		{Key: "credits.checkin_reward", Value: "3"},
		{Key: "credits.invite_enabled", Value: "true"},
		{Key: "credits.invite_reward", Value: "3"},
		{Key: "credits.cdk_redeem_enabled", Value: "true"},
		{Key: "pay.enabled", Value: "false"},
		{Key: "pay.api_base", Value: "https://pay.v8jisu.cn/api/pay"},
		{Key: "pay.methods", Value: "wxpay,alipay"},
		{Key: "pay.min_amount", Value: "1"},
		{Key: "pay.points_ratio", Value: "100"},
		{Key: "logs.retention_days", Value: "30"},
		{Key: "media.retention_days", Value: "30"},
	}
	for _, item := range defaults {
		var count int64
		if err := db.WithContext(ctx).Model(&model.SiteSetting{}).Where("key = ?", item.Key).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			continue
		}
		if err := db.WithContext(ctx).Create(&item).Error; err != nil {
			return err
		}
	}
	// Built-in Adobe video capabilities are catalog-owned (the admin form shows
	// them read-only). Backfill existing model rows after adding the media columns,
	// so an upgrade behaves the same as adding the model on a fresh install.
	if err := db.WithContext(ctx).Exec(`UPDATE model_configs SET
		max_reference_images = CASE id
			WHEN 'firefly-seedance-2' THEN 9 WHEN 'firefly-seedance-2-fast' THEN 9
			ELSE max_reference_images END,
		max_reference_videos = CASE id
			WHEN 'firefly-kling-3' THEN 1 WHEN 'firefly-kling-o3' THEN 1
			WHEN 'firefly-seedance-2' THEN 3 WHEN 'firefly-seedance-2-fast' THEN 3
			WHEN 'firefly-ray' THEN 1 WHEN 'firefly-video' THEN 1 ELSE max_reference_videos END,
		max_reference_audios = CASE id
			WHEN 'firefly-seedance-2' THEN 3 WHEN 'firefly-seedance-2-fast' THEN 3 ELSE max_reference_audios END,
		max_reference_media = CASE id
			WHEN 'firefly-seedance-2' THEN 9 WHEN 'firefly-seedance-2-fast' THEN 9
			ELSE max_reference_media END,
		reference_mode = CASE id
			WHEN 'firefly-seedance-2' THEN 'asset' WHEN 'firefly-seedance-2-fast' THEN 'asset'
			ELSE reference_mode END,
		supports_audio_output = CASE id
			WHEN 'gemini-veo31' THEN true WHEN 'firefly-kling-3' THEN true
			WHEN 'firefly-kling-o3' THEN true WHEN 'firefly-seedance-2' THEN true
			WHEN 'firefly-seedance-2-fast' THEN true ELSE supports_audio_output END
		WHERE id IN ('gemini-veo31','firefly-kling-3','firefly-kling-o3','firefly-seedance-2',
			'firefly-seedance-2-fast','firefly-ray','firefly-video')`).Error; err != nil {
		return err
	}
	// One-time backfill of the persistent per-model generation counter from
	// historical success logs, so the admin "次数" keeps its running total when we
	// switch it off the (retention-pruned) event_log. Only touches models still at
	// 0, so it never double-counts after the first run; increments take over next.
	if err := db.WithContext(ctx).Exec(
		`UPDATE model_configs m SET generation_count = COALESCE(
			(SELECT COUNT(*) FROM event_logs e WHERE e.model = m.id AND e.status = 'success'), 0)
		 WHERE m.generation_count = 0`).Error; err != nil {
		return err
	}
	// Same one-time backfill for the per-user generation counter.
	if err := db.WithContext(ctx).Exec(
		`UPDATE users u SET generation_count = COALESCE(
			(SELECT COUNT(*) FROM event_logs e WHERE e.user_id = u.id AND e.status = 'success'), 0)
		 WHERE u.generation_count = 0`).Error; err != nil {
		return err
	}
	// Seed the dashboard lifetime counters from logs ONCE (only when empty), so the
	// all-time cards start from real history then track forward via the hooks.
	var counterRows int64
	if err := db.WithContext(ctx).Model(&model.StatCounter{}).Count(&counterRows).Error; err == nil && counterRows == 0 {
		_ = db.WithContext(ctx).Exec(`INSERT INTO stat_counters (key, value, updated_at)
			SELECT 'total',   COUNT(*),                                   now() FROM event_logs
			UNION ALL SELECT 'success', COUNT(*) FILTER (WHERE status='success'), now() FROM event_logs
			UNION ALL SELECT 'failed',  COUNT(*) FILTER (WHERE status='failed'),  now() FROM event_logs
			UNION ALL SELECT 'image',   COUNT(*) FILTER (WHERE kind='image'),     now() FROM event_logs
			UNION ALL SELECT 'video',   COUNT(*) FILTER (WHERE kind='video'),     now() FROM event_logs
			UNION ALL SELECT 'api',     COUNT(*) FILTER (WHERE source='v1'),      now() FROM event_logs
			ON CONFLICT (key) DO NOTHING`).Error
	}
	return nil
}
