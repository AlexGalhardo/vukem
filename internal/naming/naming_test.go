package naming

import "testing"

func TestConversions(t *testing.T) {
	t.Parallel()

	tests := []struct{ in, snake, kebab, pascal, camel string }{
		{"UserProfile", "user_profile", "user-profile", "UserProfile", "userProfile"},
		{"HTTPCode", "http_code", "http-code", "HttpCode", "httpCode"},
		{"user_profile", "user_profile", "user-profile", "UserProfile", "userProfile"},
		{"send-reports", "send_reports", "send-reports", "SendReports", "sendReports"},
		{"OAuth2Token", "o_auth2_token", "o-auth2-token", "OAuth2Token", "oAuth2Token"},
		{"post", "post", "post", "Post", "post"},
		// Documented (site database/orm, "Naming conventions"): a plural
		// acronym splits before its last capital; changing it would rename
		// existing columns, so models use a db tag instead.
		{"ExternalIDs", "external_i_ds", "external-i-ds", "ExternalIDs", "externalIDs"},
		{"", "", "", "", ""},
	}

	for _, tt := range tests {
		if got := Snake(tt.in); got != tt.snake {
			t.Errorf("Snake(%q) = %q, want %q", tt.in, got, tt.snake)
		}
		if got := Kebab(tt.in); got != tt.kebab {
			t.Errorf("Kebab(%q) = %q, want %q", tt.in, got, tt.kebab)
		}
		if got := Pascal(tt.in); got != tt.pascal {
			t.Errorf("Pascal(%q) = %q, want %q", tt.in, got, tt.pascal)
		}
		if got := Camel(tt.in); got != tt.camel {
			t.Errorf("Camel(%q) = %q, want %q", tt.in, got, tt.camel)
		}
	}
}

func TestPlural(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{
		"post": "posts", "category": "categories", "day": "days", "box": "boxes",
		"status": "statuses", "match": "matches", "dish": "dishes", "buzz": "buzzes",
	} {
		if got := Plural(in); got != want {
			t.Errorf("Plural(%q) = %q, want %q", in, got, want)
		}
	}
}
