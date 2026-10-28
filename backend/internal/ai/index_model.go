package ai

// IndexModel returns the stable vector-space identity used for persisted
// embeddings. Providers without a separate identity retain their model name.
func IndexModel(provider Provider) string {
	if identified, ok := provider.(interface{ IndexModel() string }); ok {
		return identified.IndexModel()
	}
	return provider.Model()
}
