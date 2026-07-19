// reference_test.go 锁定 CombineConfidence 的「链路最弱合并」契约：valid 枚举取最弱，
// 空串按文档语义退让到另一端，两端皆空返回空。这是 confidence 语义的唯一真值来源，
// 此前仅被 impact/serviceimpact 间接覆盖，缺少直测。
package facts

import "testing"

func TestCombineConfidenceWeakestWins(t *testing.T) {
	cases := []struct {
		name         string
		parent, edge Confidence
		want         Confidence
	}{
		{"high_high", ConfidenceHigh, ConfidenceHigh, ConfidenceHigh},
		{"high_medium", ConfidenceHigh, ConfidenceMedium, ConfidenceMedium},
		{"medium_high", ConfidenceMedium, ConfidenceHigh, ConfidenceMedium},
		{"high_low", ConfidenceHigh, ConfidenceLow, ConfidenceLow},
		{"low_high", ConfidenceLow, ConfidenceHigh, ConfidenceLow},
		{"medium_low", ConfidenceMedium, ConfidenceLow, ConfidenceLow},
		{"low_medium", ConfidenceLow, ConfidenceMedium, ConfidenceLow},
		{"medium_medium", ConfidenceMedium, ConfidenceMedium, ConfidenceMedium},
		{"low_low", ConfidenceLow, ConfidenceLow, ConfidenceLow},
		// 空串退让：parent 空取 edge，edge 空取 parent，两者皆空返回空。
		{"empty_parent", "", ConfidenceHigh, ConfidenceHigh},
		{"empty_edge", ConfidenceLow, "", ConfidenceLow},
		{"both_empty", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CombineConfidence(tc.parent, tc.edge); got != tc.want {
				t.Fatalf("CombineConfidence(%q, %q) = %q, want %q", tc.parent, tc.edge, got, tc.want)
			}
		})
	}
}

// TestCombineConfidenceIsCommutativeForWeakest 断言合并对 valid 枚举满足交换律：
// 无论 parent/edge 顺序，结果都取最弱值。这保证跨根/跨文件合并与树路径独立计算结果一致。
func TestCombineConfidenceIsCommutativeForWeakest(t *testing.T) {
	valid := []Confidence{ConfidenceHigh, ConfidenceMedium, ConfidenceLow}
	for _, a := range valid {
		for _, b := range valid {
			if CombineConfidence(a, b) != CombineConfidence(b, a) {
				t.Fatalf("CombineConfidence not commutative for %q,%q", a, b)
			}
		}
	}
}

// TestCombineConfidenceSurfacesMalformedValues 验证非空但不属于 low/medium/high 三档
// 合法值的畸形 confidence（如拼写错误、未来新增枚举值但常量定义未同步）不会被当作
// "未设置"静默让位给另一跳，而是原样透传出来。
//
// 修复前 rank 对未知非空值返回 0（与"空串/未设置"同值），CombineConfidence 会误判
// "空则让位"分支为真，让畸形值消失、只返回另一跳的值——若另一跳恰好是 high，
// 数据 bug 会被完全掩盖成一个看似正常的 high 结论。这里验证畸形值本身会出现在结果
// 中（便于从输出定位问题数据），且它总是被视为链路最弱一跳（不会让更强的一跳错误
// 胜出）。
func TestCombineConfidenceSurfacesMalformedValues(t *testing.T) {
	const malformed Confidence = "extreme" // 拼写错误/非法值的示例，不属于三档合法枚举
	cases := []struct {
		name         string
		parent, edge Confidence
		want         Confidence
	}{
		// 畸形值与合法值搭配：畸形值作为"链路最弱一跳"透传出来，而不是被合法值掩盖。
		{"malformed_parent_vs_high", malformed, ConfidenceHigh, malformed},
		{"high_vs_malformed_edge", ConfidenceHigh, malformed, malformed},
		{"malformed_parent_vs_low", malformed, ConfidenceLow, malformed},
		// 两端都畸形：任取一端返回（这里约定返回 parent，与 pr<=er 时取 parent 的既有分支一致）。
		{"both_malformed", malformed, malformed, malformed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CombineConfidence(tc.parent, tc.edge); got != tc.want {
				t.Fatalf("CombineConfidence(%q, %q) = %q, want %q (malformed value must not be silently dropped)", tc.parent, tc.edge, got, tc.want)
			}
		})
	}
}
