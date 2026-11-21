package oauth_test

import (
	"errors"
	"testing"

	"github.com/AlexGalhardo/vukem/oauth"
)

func TestMetadataDocumentRefusesDotSegments(t *testing.T) {
	t.Parallel()

	for _, id := range []string{
		"https://app.example.com/a/../client.json",
		"https://app.example.com/./client.json",
		"https://app.example.com/a/%2e%2e/client.json",
		"https://app.example.com/client/..",
	} {
		if _, err := (&oauth.MetadataDocuments{}).Client(t.Context(), id); !errors.Is(err, oauth.ErrUnknownClient) {
			t.Errorf("%s: err = %v, want ErrUnknownClient", id, err)
		}
	}
}
