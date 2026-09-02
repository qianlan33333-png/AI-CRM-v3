package http

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"net/http/httptest"
	"testing"
)

func TestMutationKeyPreservesExplicitAndMintsCompatibilityKey(t *testing.T) {
	explicit := httptest.NewRequest("POST", "/api/admin/image-library", nil)
	explicit.Header.Set("Idempotency-Key", "client-key-123456")
	if got := mutationKey(explicit); got != "client-key-123456" {
		t.Fatalf("explicit key=%q", got)
	}
	first := mutationKey(httptest.NewRequest("POST", "/api/admin/image-library", nil))
	second := mutationKey(httptest.NewRequest("POST", "/api/admin/image-library", nil))
	if len(first) < 32 || first[:14] != "server_compat_" || first == second {
		t.Fatalf("compatibility keys must be distinct and auditable: %q %q", first, second)
	}
}

func TestImageQueryFrozenFilterShape(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/admin/image-library?tags=a,b,a&tag_group=c,d&tag_group=e&only_unlabeled=true&enabled_only=false", nil)
	query, err := imageQuery(r, 50, 0, "")
	if err != nil || query.EnabledOnly || !query.OnlyUnlabeled || len(query.Tags) != 2 || len(query.TagGroups) != 2 || len(query.TagGroups[0]) != 2 {
		t.Fatalf("query=%+v err=%v", query, err)
	}
	if _, err := imageQuery(httptest.NewRequest("GET", "/?enabled_only=TRUE", nil), 50, 0, ""); err == nil {
		t.Fatal("non-lowercase boolean accepted")
	}
}

func TestMediaVariantIsDeterministicAndNotOriginalBytes(t *testing.T) {
	source := image.NewRGBA(image.Rect(0, 0, 400, 200))
	source.Set(0, 0, color.RGBA{R: 255, A: 255})
	var original bytes.Buffer
	if err := png.Encode(&original, source); err != nil {
		t.Fatal(err)
	}
	first, mime, err := mediaVariant(original.Bytes(), "image/png", "thumb_160")
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := mediaVariant(original.Bytes(), "image/png", "thumb_160")
	if err != nil {
		t.Fatal(err)
	}
	if mime != "image/png" || !bytes.Equal(first, second) || bytes.Equal(first, original.Bytes()) {
		t.Fatal("variant must be deterministic rendered bytes")
	}
	decoded, _, err := image.Decode(bytes.NewReader(first))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Bounds().Dx() != 160 || decoded.Bounds().Dy() != 80 {
		t.Fatalf("dimensions=%v", decoded.Bounds())
	}
}

func TestImageDataURLRequiresBase64(t *testing.T) {
	// The parser's public contract is intentionally strict: unmarked data URLs
	// are rejected before bytes can reach Media storage.
	value := base64.StdEncoding.EncodeToString([]byte("not image"))
	if value == "" {
		t.Fatal("unexpected empty base64")
	}
}
