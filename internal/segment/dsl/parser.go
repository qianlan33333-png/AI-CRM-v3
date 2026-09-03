package dsl

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
)

var ErrInvalid = errors.New("invalid closed audience definition")

type wireDefinition struct {
	SchemaVersion int                        `json:"schema_version"`
	TemplateKey   Template                   `json:"template_key"`
	Parameters    map[string]json.RawMessage `json:"parameters"`
}

func Parse(raw json.RawMessage) (AST, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var wire wireDefinition
	if decoder.Decode(&wire) != nil {
		return AST{}, ErrInvalid
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) || wire.SchemaVersion != 1 || len(wire.Parameters) != 1 {
		return AST{}, ErrInvalid
	}
	field, parameter, op := "", "", "in"
	switch wire.TemplateKey {
	case ActiveContacts:
		field, parameter, op = "customer.active_within_days", "within_days", "lte"
	case StageAny:
		field, parameter = "customer.stage", "stages"
	case TagAny:
		field, parameter = "tag.code", "tag_codes"
	case OwnerAny:
		field, parameter = "owner.staff_id", "staff_ids"
	case ChannelAny:
		field, parameter = "channel.code", "channels"
	default:
		return AST{}, ErrInvalid
	}
	value, ok := wire.Parameters[parameter]
	if !ok {
		return AST{}, ErrInvalid
	}
	values := []string{}
	if wire.TemplateKey == ActiveContacts {
		var days string
		if json.Unmarshal(value, &days) != nil {
			return AST{}, ErrInvalid
		}
		n, err := strconv.Atoi(days)
		if err != nil || n < 1 || n > 999 {
			return AST{}, ErrInvalid
		}
		values = []string{days}
	} else if json.Unmarshal(value, &values) != nil || len(values) == 0 || len(values) > 100 {
		return AST{}, ErrInvalid
	}
	seen := map[string]struct{}{}
	for _, value := range values {
		if value == "" || value != strings.TrimSpace(value) || len(value) > 200 {
			return AST{}, ErrInvalid
		}
		if _, duplicate := seen[value]; duplicate {
			return AST{}, ErrInvalid
		}
		seen[value] = struct{}{}
	}
	return AST{SchemaVersion: 1, Template: wire.TemplateKey, Predicate: Predicate{Field: field, Op: op, Values: values}}, nil
}
