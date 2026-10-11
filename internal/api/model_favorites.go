package api

// ModelFavorites is the saved set for one config path and provider.
type ModelFavorites struct {
	ScopeID    string   `json:"scopeId"`
	ProviderID string   `json:"providerId"`
	Revision   uint64   `json:"revision"`
	ModelIDs   []string `json:"modelIds"`
}

// SetModelFavoriteRequest applies an explicit desired value to one model.
type SetModelFavoriteRequest struct {
	CoreID     string `json:"coreId"`
	SessionID  string `json:"sessionId,omitempty"`
	Workspace  string `json:"workspace,omitempty"`
	ProviderID string `json:"providerId"`
	ModelID    string `json:"modelId"`
	Favorite   *bool  `json:"favorite"`
}

// ModelFavoriteResult reports the saved snapshot and the requested value.
type ModelFavoriteResult struct {
	ModelFavorites
	ModelID  string `json:"modelId"`
	Favorite bool   `json:"favorite"`
}
