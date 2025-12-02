package routing

import (
	"os"
	"testing"
)

var benchmarkConfig *RouteConfig

func BenchmarkParseSmallConfig(b *testing.B) {
	yamlData, err := os.ReadFile("testdata/minimal.yaml")
	if err != nil {
		b.Fatal(err)
	}

	parser := NewRouteConfigParser()
	var config *RouteConfig

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		config, err = parser.Parse(yamlData)
		if err != nil {
			b.Fatal(err)
		}
	}
	benchmarkConfig = config // Prevent optimization
}

func BenchmarkParseMediumConfig(b *testing.B) {
	yamlData, err := os.ReadFile("testdata/production.yaml")
	if err != nil {
		b.Fatal(err)
	}

	parser := NewRouteConfigParser()
	var config *RouteConfig

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		config, err = parser.Parse(yamlData)
		if err != nil {
			b.Fatal(err)
		}
	}
	benchmarkConfig = config
}

func BenchmarkGetReceiver(b *testing.B) {
	config := &RouteConfig{
		ReceiverIndex: map[string]*Receiver{
			"webhook1": {Name: "webhook1"},
			"webhook2": {Name: "webhook2"},
			"webhook3": {Name: "webhook3"},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = config.GetReceiver("webhook2")
	}
}

func BenchmarkReceiverClone(b *testing.B) {
	receiver := &Receiver{
		Name: "webhook1",
		WebhookConfigs: []*WebhookConfig{
			{URL: "https://example.com/webhook"},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = receiver.Clone()
	}
}

func BenchmarkReceiverSanitize(b *testing.B) {
	receiver := &Receiver{
		Name: "webhook1",
		WebhookConfigs: []*WebhookConfig{
			{
				URL: "https://webhook.site/xxx?token=secret123",
				HTTPHeaders: map[string]string{
					"Authorization": "Bearer secret_token",
				},
			},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = receiver.Sanitize()
	}
}
