package github

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/bdobrica/ThinkPixelTG/internal/ports"
)

const maximumProviderEvidenceValueBytes = 256

func responseEvidence(response *http.Response) ports.ConnectorEvidence {
	metadata, _ := json.Marshal(struct {
		StatusCode int `json:"status_code"`
	}{StatusCode: response.StatusCode})
	return ports.ConnectorEvidence{
		ProviderRequestID: boundedHeader(response.Header.Get("X-GitHub-Request-Id")),
		ResourceVersion:   boundedHeader(response.Header.Get("ETag")),
		SafeMetadata:      metadata,
	}
}

func boundedHeader(value string) string {
	if value == "" || len(value) > maximumProviderEvidenceValueBytes || strings.IndexFunc(value, func(character rune) bool {
		return character < 0x20 || character == 0x7f
	}) >= 0 {
		return ""
	}
	return value
}
