package dataformat

type StoreTOML struct {
	Data map[string]interface{} `json:"metadata" toml:"metadata"`
}
