package leonardo

import "testing"

// 普通号(免费,plan=BASIC,只有每日 150 免费 token)必须仍被判为非积分号 —— 它继续走
// 每日重置逻辑;只有出现明确的付费证据(购买/结转点数、付费 plan、订阅点数超过免费
// 上限)才算积分号。plan 无法识别时按普通号处理,不改变原有行为。
func TestIsPaidPlan(t *testing.T) {
	cases := []struct {
		name                 string
		plan                 string
		subs, paid, rollover int
		want                 bool
	}{
		{"free basic", "BASIC", 150, 0, 0, false},
		{"free partially spent", "BASIC", 60, 0, 0, false},
		{"unknown plan, free-size balance", "SOMETHING_NEW", 150, 0, 0, false},
		{"purchased tokens", "BASIC", 0, 3000, 0, true},
		{"rollover tokens", "BASIC", 0, 0, 120, true},
		{"subscription above free cap", "", 8500, 0, 0, true},
		{"paid plan", "ARTISAN", 0, 0, 0, true},
		{"annual paid plan", "maestro_annual", 0, 0, 0, true},
	}
	for _, c := range cases {
		if got := IsPaidPlan(c.plan, c.subs, c.paid, c.rollover); got != c.want {
			t.Errorf("%s: IsPaidPlan(%q, %d, %d, %d) = %v, want %v", c.name, c.plan, c.subs, c.paid, c.rollover, got, c.want)
		}
	}
}
