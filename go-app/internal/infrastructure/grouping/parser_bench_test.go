package grouping

import (
	"testing"
)

// Sample YAML configurations for benchmarking
const (
	simpleYAML = `
route:
  receiver: "team-X"
  group_by: ['alertname', 'cluster']
  group_wait: 30s
  group_interval: 5m
  repeat_interval: 4h
`

	complexYAML = `
route:
  receiver: "default"
  group_by: ['alertname', 'cluster']
  group_wait: 30s
  group_interval: 5m
  repeat_interval: 4h
  match:
    severity: critical
    team: backend
  match_re:
    service: "^api-.*"
  routes:
    - receiver: "team-frontend"
      group_by: ['cluster', 'namespace']
      group_wait: 15s
      match:
        team: frontend
      continue: true
      routes:
        - receiver: "frontend-critical"
          group_by: ['pod']
          match:
            severity: critical
    - receiver: "team-backend"
      group_by: ['service']
      match_re:
        service: "^api-.*"
      routes:
        - receiver: "backend-database"
          group_by: ['database']
          match:
            component: database
`

	deeplyNestedYAML = `
route:
  receiver: "root"
  group_by: ['alertname']
  routes:
    - receiver: "level1"
      group_by: ['cluster']
      routes:
        - receiver: "level2"
          group_by: ['namespace']
          routes:
            - receiver: "level3"
              group_by: ['pod']
              routes:
                - receiver: "level4"
                  group_by: ['container']
                  routes:
                    - receiver: "level5"
                      group_by: ['severity']
`
)

// BenchmarkParser_Parse_Simple benchmarks parsing a simple configuration
func BenchmarkParser_Parse_Simple(b *testing.B) {
	parser := NewParser()
	data := []byte(simpleYAML)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := parser.Parse(data)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkParser_Parse_Complex benchmarks parsing a complex configuration
func BenchmarkParser_Parse_Complex(b *testing.B) {
	parser := NewParser()
	data := []byte(complexYAML)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := parser.Parse(data)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkParser_Parse_DeeplyNested benchmarks parsing deeply nested routes
func BenchmarkParser_Parse_DeeplyNested(b *testing.B) {
	parser := NewParser()
	data := []byte(deeplyNestedYAML)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := parser.Parse(data)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkParser_ParseString benchmarks string parsing
func BenchmarkParser_ParseString(b *testing.B) {
	parser := NewParser()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := parser.ParseString(simpleYAML)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkApplyRouteDefaults benchmarks default application
func BenchmarkApplyRouteDefaults(b *testing.B) {
	route := &Route{
		Receiver: "test",
		GroupBy:  []string{"alertname"},
		Routes: []*Route{
			{
				Receiver: "nested1",
				GroupBy:  []string{"cluster"},
			},
			{
				Receiver: "nested2",
				GroupBy:  []string{"namespace"},
			},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		applyRouteDefaults(route)
	}
}

// BenchmarkCalculateRouteDepth benchmarks depth calculation
func BenchmarkCalculateRouteDepth(b *testing.B) {
	// Create a route with depth 5
	route := &Route{
		Receiver: "root",
		GroupBy:  []string{"alertname"},
	}

	current := route
	for i := 0; i < 4; i++ {
		nested := &Route{
			Receiver: "nested",
			GroupBy:  []string{"label"},
		}
		current.Routes = []*Route{nested}
		current = nested
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = calculateRouteDepth(route)
	}
}

// BenchmarkValidateSemantics benchmarks semantic validation
func BenchmarkValidateSemantics(b *testing.B) {
	parser := NewParser()
	config := &GroupingConfig{
		Route: &Route{
			Receiver: "test",
			GroupBy:  []string{"alertname", "cluster", "namespace"},
			Routes: []*Route{
				{
					Receiver: "nested1",
					GroupBy:  []string{"pod"},
				},
				{
					Receiver: "nested2",
					GroupBy:  []string{"container"},
				},
			},
		},
	}
	applyRouteDefaults(config.Route)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = parser.validateSemantics(config)
	}
}

// BenchmarkRoute_Clone benchmarks route cloning
func BenchmarkRoute_Clone(b *testing.B) {
	route := &Route{
		Receiver:       "test",
		GroupBy:        []string{"alertname", "cluster"},
		GroupWait:      &Duration{30},
		GroupInterval:  &Duration{300},
		RepeatInterval: &Duration{14400},
		Match:          map[string]string{"severity": "critical"},
		MatchRE:        map[string]string{"service": "^api-.*"},
		Continue:       true,
		Routes: []*Route{
			{
				Receiver: "nested",
				GroupBy:  []string{"namespace"},
			},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = route.Clone()
	}
}

// BenchmarkRoute_Validate benchmarks route validation
func BenchmarkRoute_Validate(b *testing.B) {
	route := &Route{
		Receiver: "test",
		GroupBy:  []string{"alertname", "cluster"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = route.Validate()
	}
}

// BenchmarkDuration_UnmarshalYAML benchmarks duration unmarshaling
func BenchmarkDuration_UnmarshalYAML(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var d Duration
		_ = d.UnmarshalYAML(func(v interface{}) error {
			*v.(*string) = "30s"
			return nil
		})
	}
}

// BenchmarkDuration_MarshalYAML benchmarks duration marshaling
func BenchmarkDuration_MarshalYAML(b *testing.B) {
	d := Duration{30}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = d.MarshalYAML()
	}
}

// BenchmarkParser_Parse_Parallel benchmarks parallel parsing
func BenchmarkParser_Parse_Parallel(b *testing.B) {
	parser := NewParser()
	data := []byte(complexYAML)

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := parser.Parse(data)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkRoute_Clone_Parallel benchmarks parallel cloning
func BenchmarkRoute_Clone_Parallel(b *testing.B) {
	route := &Route{
		Receiver:       "test",
		GroupBy:        []string{"alertname", "cluster"},
		GroupWait:      &Duration{30},
		GroupInterval:  &Duration{300},
		RepeatInterval: &Duration{14400},
		Match:          map[string]string{"severity": "critical"},
		Routes: []*Route{
			{
				Receiver: "nested",
				GroupBy:  []string{"namespace"},
			},
		},
	}

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = route.Clone()
		}
	})
}
