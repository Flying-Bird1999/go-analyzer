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
