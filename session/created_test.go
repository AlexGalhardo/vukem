package session

import (
	"encoding/json"
	"testing"
	"time"
)

func TestCreatedAt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		s      *Session
		want   time.Time
		wantOK bool
	}{
		{"int64", newSession("", map[string]any{keyCreated: int64(100)}), time.Unix(100, 0), true},
		{"decoded", newSession("", map[string]any{keyCreated: json.Number("200")}), time.Unix(200, 0), true},
		{"unsaved", newSession("", nil), time.Time{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := CreatedAt(tt.s)
			if !got.Equal(tt.want) || ok != tt.wantOK {
				t.Errorf("CreatedAt() = %v, %v, want %v, %v", got, ok, tt.want, tt.wantOK)
			}
		})
	}

	if _, ok := CreatedAt(nil); ok {
		t.Error("CreatedAt(nil) ok = true, want false")
	}
}
