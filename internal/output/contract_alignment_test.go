// contract_alignment_test.go 是 facts 公开契约的对齐护栏：用反射断言 Document 结构体、
// facts JSON Schema（required / properties / 各 $def）与 RenderJSON 输出四处保持同步，
// 并断言 transient 事实不会泄漏到公开 facts JSON。新增/修改公开 fact 时若漏改其中一处，
// 这里会立即失败，而不是让 golden 继续逐字节相等后静默漂移。
package output

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"

	"gopkg.inshopline.com/bff/go-analyzer/internal/facts"
)

// factsTopLevelKeys 是公开 facts JSON 必须包含且仅包含的顶层键。
// transient 事实（changes / module_changes / module_usages / route_group_flows）不在此列。
func factsTopLevelKeys() []string {
	return []string{
		"project", "symbols", "annotations", "route_groups", "routes",
		"middleware", "references", "modules", "im_events", "grpc_operations", "grpc_calls", "grpc_providers", "dubbo_providers", "job_registrations", "links", "diagnostics",
	}
}

// jsonFieldNames 返回结构体类型所有导出字段且 json tag 非 "-" 的字段名集合。
func jsonFieldNames(t reflect.Type) map[string]bool {
	out := map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		out[name] = true
	}
	return out
}

// schemaKeys 把 schema 片段的 properties map 归一化为排序后的键切片。
func schemaKeys(def map[string]any) []string {
	props, ok := def["properties"].(map[string]any)
	if !ok {
		return nil
	}
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TestFactsDocumentAlignsSchemaTopLevel 断言 Document 字段 == facts schema required ==
// facts schema properties。三者必须一致，否则新增/退役公开 fact 时会立即暴露漂移。
func TestFactsDocumentAlignsSchemaTopLevel(t *testing.T) {
	want := factsTopLevelKeys()
	sort.Strings(want)

	documentFields := keysSorted(jsonFieldNames(reflect.TypeOf(Document{})))

	factsSchema := schemaDocuments["facts"]
	required := append([]string{}, factsSchema["required"].([]string)...)
	sort.Strings(required)
	properties := schemaKeys(factsSchema)

	if !equalStringSlices(documentFields, want) {
		t.Errorf("Document json fields = %#v, want %#v", documentFields, want)
	}
	if !equalStringSlices(required, want) {
		t.Errorf("schema facts required = %#v, want %#v", required, want)
	}
	if !equalStringSlices(properties, want) {
		t.Errorf("schema facts properties = %#v, want %#v", properties, want)
	}
}

// TestRenderJSONEmitsExactlyPublicFacts 断言 RenderJSON 输出的顶层键恰好是公开 facts 键集合，
// 且 transient 事实（含被显式赋值的 Changes / ModuleChanges / ModuleUsages / RouteGroupFlows）
// 不会泄漏到公开 facts JSON。
func TestRenderJSONEmitsExactlyPublicFacts(t *testing.T) {
	store := facts.NewStore("/tmp/project", "example.com/project")
	// 显式给 transient 切片塞入数据，确保即便有数据也不会出现在公开输出。
	store.Changes = append(store.Changes, facts.ChangeFact{ID: "change:transient"})
	store.ModuleChanges = append(store.ModuleChanges, facts.ModuleChangeFact{ID: "module_change:transient"})
	store.ModuleUsages = append(store.ModuleUsages, facts.ModuleUsageFact{ID: "module_usage:transient"})
	store.RouteGroupFlows = append(store.RouteGroupFlows, facts.RouteGroupFlowFact{})

	out, err := RenderJSON(store)
	if err != nil {
		t.Fatalf("RenderJSON: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	got := make([]string, 0, len(raw))
	for k := range raw {
		got = append(got, k)
	}
	sort.Strings(got)
	want := factsTopLevelKeys()
	sort.Strings(want)
	if !equalStringSlices(got, want) {
		t.Errorf("RenderJSON top-level keys = %#v, want %#v (transient facts leaked?)", got, want)
	}
}

// TestFactsFactStructsAlignSchemaDefinitions 对每个公开 fact 类型断言其 Go 结构体 json 字段集合
// == schema 对应 $def 的 properties 集合。新增/重命名字段时漏改 schema 会立即失败。
func TestFactsFactStructsAlignSchemaDefinitions(t *testing.T) {
	defs := schemaDocuments["facts"]["$defs"].(map[string]any)
	cases := map[string]reflect.Type{
		"project":             reflect.TypeOf(facts.ProjectFact{}),
		"build_context":       reflect.TypeOf(facts.BuildContextFact{}),
		"symbol":              reflect.TypeOf(facts.SymbolFact{}),
		"annotation":          reflect.TypeOf(facts.AnnotationFact{}),
		"route_group":         reflect.TypeOf(facts.RouteGroupFact{}),
		"route":               reflect.TypeOf(facts.RouteRegistrationFact{}),
		"middleware":          reflect.TypeOf(facts.MiddlewareBindingFact{}),
		"reference":           reflect.TypeOf(facts.ReferenceFact{}),
		"module":              reflect.TypeOf(facts.ModuleDependencyFact{}),
		"im_event":            reflect.TypeOf(facts.IMEventFact{}),
		"im_event_dependency": reflect.TypeOf(facts.IMEventDependency{}),
		"im_event_evidence":   reflect.TypeOf(facts.IMEventEvidence{}),
		"grpc_operation":      reflect.TypeOf(facts.GrpcOperationFact{}),
		"grpc_client_binding": reflect.TypeOf(facts.GrpcClientBinding{}),
		"grpc_call":           reflect.TypeOf(facts.GrpcCallFact{}),
		"grpc_provider":       reflect.TypeOf(facts.GrpcProviderFact{}),
		"dubbo_provider":      reflect.TypeOf(facts.DubboProviderFact{}),
		"job_registration":    reflect.TypeOf(facts.JobRegistrationFact{}),
		"link":                reflect.TypeOf(facts.LinkFact{}),
		"diagnostic":          reflect.TypeOf(facts.DiagnosticFact{}),
		"source_span":         reflect.TypeOf(facts.SourceSpan{}),
		"wrapper":             reflect.TypeOf(facts.WrapperFact{}),
		"evidence":            reflect.TypeOf(facts.EvidenceFact{}),
	}
	for name, typ := range cases {
		name, typ := name, typ
		t.Run(name, func(t *testing.T) {
			def, ok := defs[name].(map[string]any)
			if !ok {
				t.Fatalf("schema $defs missing %q", name)
			}
			want := keysSorted(jsonFieldNames(typ))
			got := schemaKeys(def)
			if !equalStringSlices(got, want) {
				t.Errorf("%s: schema properties = %#v, struct json fields = %#v", name, got, want)
			}
		})
	}
}

// TestFactsSchemaRequiredAlignsOmitempty 断言 schema required 集合与 Go json tag
// 的 omitempty 语义一致：非 omitempty 字段必须 required，omitempty 字段不能 required。
func TestFactsSchemaRequiredAlignsOmitempty(t *testing.T) {
	defs := schemaDocuments["facts"]["$defs"].(map[string]any)
	cases := map[string]reflect.Type{
		"project":             reflect.TypeOf(facts.ProjectFact{}),
		"build_context":       reflect.TypeOf(facts.BuildContextFact{}),
		"symbol":              reflect.TypeOf(facts.SymbolFact{}),
		"annotation":          reflect.TypeOf(facts.AnnotationFact{}),
		"route_group":         reflect.TypeOf(facts.RouteGroupFact{}),
		"route":               reflect.TypeOf(facts.RouteRegistrationFact{}),
		"middleware":          reflect.TypeOf(facts.MiddlewareBindingFact{}),
		"reference":           reflect.TypeOf(facts.ReferenceFact{}),
		"module":              reflect.TypeOf(facts.ModuleDependencyFact{}),
		"im_event":            reflect.TypeOf(facts.IMEventFact{}),
		"im_event_dependency": reflect.TypeOf(facts.IMEventDependency{}),
		"im_event_evidence":   reflect.TypeOf(facts.IMEventEvidence{}),
		"grpc_operation":      reflect.TypeOf(facts.GrpcOperationFact{}),
		"grpc_client_binding": reflect.TypeOf(facts.GrpcClientBinding{}),
		"grpc_call":           reflect.TypeOf(facts.GrpcCallFact{}),
		"grpc_provider":       reflect.TypeOf(facts.GrpcProviderFact{}),
		"dubbo_provider":      reflect.TypeOf(facts.DubboProviderFact{}),
		"job_registration":    reflect.TypeOf(facts.JobRegistrationFact{}),
		"link":                reflect.TypeOf(facts.LinkFact{}),
		"diagnostic":          reflect.TypeOf(facts.DiagnosticFact{}),
		"source_span":         reflect.TypeOf(facts.SourceSpan{}),
		"wrapper":             reflect.TypeOf(facts.WrapperFact{}),
		"evidence":            reflect.TypeOf(facts.EvidenceFact{}),
	}
	for name, typ := range cases {
		name, typ := name, typ
		t.Run(name, func(t *testing.T) {
			def, ok := defs[name].(map[string]any)
			if !ok {
				t.Fatalf("schema $defs missing %q", name)
			}
			got := schemaRequired(def)
			want := requiredJSONFieldNames(typ)
			if !equalStringSlices(got, want) {
				t.Errorf("%s: schema required = %#v, non-omitempty struct fields = %#v", name, got, want)
			}
		})
	}
}

// TestImpactDocumentStructsAlignSchemaDefinitions 断言 impact 与 grpc-impact 输出文档
// 各结构体的 json 字段集合 == 对应 schema $def 的 properties 集合。这道护栏覆盖对外
// impact/grpc-impact 契约（此前只有 facts 有对齐护栏），防止如 endpoint_summary.routes
// 这类结构体新增字段却漏改 schema 的静默漂移再次发生。
func TestImpactDocumentStructsAlignSchemaDefinitions(t *testing.T) {
	type docCase struct {
		schema string
		defs   map[string]reflect.Type
	}
	cases := []docCase{
		{
			schema: "impact",
			defs: map[string]reflect.Type{
				"endpoint_summary":             reflect.TypeOf(EndpointSummary{}),
				"dependency_endpoint":          reflect.TypeOf(dependencyEndpoint{}),
				"dependency_symbol":            reflect.TypeOf(dependencySymbol{}),
				"dependency_client":            reflect.TypeOf(dependencyClient{}),
				"dependency_call_site":         reflect.TypeOf(dependencyCallSite{}),
				"dependency_chain":             reflect.TypeOf(dependencyChain{}),
				"endpoint_impact_source":       reflect.TypeOf(EndpointImpactSource{}),
				"endpoint_root_symbol_summary": reflect.TypeOf(EndpointRootSymbolSummary{}),
				"endpoint_source_summary":      reflect.TypeOf(EndpointSourceSummary{}),
				"file_source_impact":           reflect.TypeOf(FileSourceImpact{}),
				"grpc_consumer_impact":         reflect.TypeOf(GrpcConsumerImpact{}),
				"grpc_operation_summary":       reflect.TypeOf(GrpcOperationSummary{}),
				"grpc_source_impact":           reflect.TypeOf(GrpcSourceImpact{}),
				"impact_node":                  reflect.TypeOf(ImpactNode{}),
				"impact_summary":               reflect.TypeOf(ImpactSummary{}),
				"module_source_impact":         reflect.TypeOf(ModuleSourceImpact{}),
				"module_replacement":           reflect.TypeOf(ModuleReplacement{}),
			},
		},
		{
			schema: "grpc-impact",
			defs: map[string]reflect.Type{
				"service_contract_summary":            reflect.TypeOf(ServiceContractSummary{}),
				"contract_registration_summary":       reflect.TypeOf(ContractRegistrationSummary{}),
				"contract_source_summary":             reflect.TypeOf(ContractSourceSummary{}),
				"service_entry_impact_groups":         reflect.TypeOf(ServiceEntryImpactGroups{}),
				"service_entry_file_source_impact":    reflect.TypeOf(GrpcFileSourceImpact{}),
				"service_entry_module_source_impact":  reflect.TypeOf(GrpcModuleSourceImpact{}),
				"service_entry_source_summary_groups": reflect.TypeOf(ServiceEntrySourceSummaryGroups{}),
				"service_entry_impact_source":         reflect.TypeOf(ServiceEntryImpactSource{}),
				"endpoint_root_symbol_summary":        reflect.TypeOf(EndpointRootSymbolSummary{}),
				"impact_node":                         reflect.TypeOf(ImpactNode{}),
				"module_replacement":                  reflect.TypeOf(ModuleReplacement{}),
			},
		},
	}
	for _, dc := range cases {
		defs := schemaDocuments[dc.schema]["$defs"].(map[string]any)
		for name, typ := range dc.defs {
			name, typ := name, typ
			t.Run(dc.schema+"/"+name, func(t *testing.T) {
				def, ok := defs[name].(map[string]any)
				if !ok {
					t.Fatalf("%s schema $defs missing %q", dc.schema, name)
				}
				want := keysSorted(jsonFieldNames(typ))
				got := schemaKeys(def)
				if !equalStringSlices(got, want) {
					t.Errorf("%s/%s: schema properties = %#v, struct json fields = %#v", dc.schema, name, got, want)
				}
			})
		}
	}
}

// keysSorted 返回 map 键的排序切片，便于稳定比较。
func keysSorted(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func requiredJSONFieldNames(t reflect.Type) []string {
	out := map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := f.Tag.Get("json")
		name := strings.Split(tag, ",")[0]
		if name == "" || name == "-" || strings.Contains(tag, "omitempty") {
			continue
		}
		out[name] = true
	}
	return keysSorted(out)
}

func schemaRequired(def map[string]any) []string {
	raw, ok := def["required"].([]string)
	if !ok {
		return nil
	}
	out := append([]string(nil), raw...)
	sort.Strings(out)
	return out
}

// equalStringSlices 判断两个排序后的字符串切片是否相等。
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
