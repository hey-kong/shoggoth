package model

type Model struct {
	Format  string                 `json:"format,omitempty"`
	URL     string                 `json:"url,omitempty"`
	Metrics map[string]interface{} `json:"metrics,omitempty"`
}

func (m *Model) GetURL() string {
	return m.URL
}
