package dsl

type Template string

const (
	ActiveContacts Template = "active_contacts"
	StageAny       Template = "stage_any"
	TagAny         Template = "tag_any"
	OwnerAny       Template = "owner_any"
	ChannelAny     Template = "channel_any"
)

type Predicate struct {
	Field  string   `json:"field"`
	Op     string   `json:"op"`
	Values []string `json:"values"`
}

type AST struct {
	SchemaVersion int       `json:"schema_version"`
	Template      Template  `json:"template"`
	Predicate     Predicate `json:"predicate"`
}
