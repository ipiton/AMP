package services

// EnrichmentMode represents enrichment mode
type EnrichmentMode string

const (
	EnrichmentModeEnriched                        EnrichmentMode = "enriched"
	EnrichmentModeTransparent                     EnrichmentMode = "transparent"
	EnrichmentModeTransparentWithRecommendations EnrichmentMode = "transparent_with_recommendations"
)

// EnrichmentModeManager manages enrichment modes
type EnrichmentModeManager struct{}

// GetMode returns current enrichment mode
func (e *EnrichmentModeManager) GetMode(ctx interface{}) (EnrichmentMode, error) {
	return EnrichmentModeEnriched, nil
}
