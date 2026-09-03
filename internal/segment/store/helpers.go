package store

import "strings"

func unique(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique")
}
