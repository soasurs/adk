package message

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soasurs/adk/model"
)

// TestParts_ValueScan verifies that Parts serializes to JSON and deserializes back correctly.
func TestParts_ValueScan(t *testing.T) {
	original := Parts{
		{Type: model.ContentPartTypeText, Text: "hello"},
		{
			Type:        model.ContentPartTypeImageURL,
			ImageURL:    "https://example.com/img.png",
			ImageDetail: model.ImageDetailHigh,
		},
		{
			Type:        model.ContentPartTypeImageBase64,
			ImageBase64: "abc123",
			MIMEType:    "image/jpeg",
		},
	}

	val, err := original.Value()
	require.NoError(t, err)

	var restored Parts
	err = restored.Scan(val)
	require.NoError(t, err)

	require.Len(t, restored, len(original))
	assert.Equal(t, original[0].Type, restored[0].Type)
	assert.Equal(t, original[0].Text, restored[0].Text)
	assert.Equal(t, original[1].ImageURL, restored[1].ImageURL)
	assert.Equal(t, original[1].ImageDetail, restored[1].ImageDetail)
	assert.Equal(t, original[2].ImageBase64, restored[2].ImageBase64)
	assert.Equal(t, original[2].MIMEType, restored[2].MIMEType)
}

// TestParts_ScanNil verifies that scanning a nil value yields an empty Parts slice.
func TestParts_ScanNil(t *testing.T) {
	var p Parts
	err := p.Scan(nil)
	require.NoError(t, err)
	assert.Empty(t, p)
}

// TestParts_ScanBytes verifies that scanning a []byte value works.
func TestParts_ScanBytes(t *testing.T) {
	var p Parts
	err := p.Scan([]byte(`[{"Type":"text","Text":"hi"}]`))
	require.NoError(t, err)
	require.Len(t, p, 1)
	assert.Equal(t, model.ContentPartTypeText, p[0].Type)
	assert.Equal(t, "hi", p[0].Text)
}

// TestFromModel_ToModel_Parts verifies that ContentParts survive a full FromModel → ToModel round-trip.
func TestFromModel_ToModel_Parts(t *testing.T) {
	original := model.Message{
		Role: model.RoleUser,
		Parts: []model.ContentPart{
			{Type: model.ContentPartTypeText, Text: "describe this"},
			{
				Type:        model.ContentPartTypeImageURL,
				ImageURL:    "https://example.com/cat.jpg",
				ImageDetail: model.ImageDetailAuto,
			},
		},
	}

	persisted := FromModel(original)
	require.Len(t, persisted.Parts, 2)

	restored := persisted.ToModel()
	require.Len(t, restored.Parts, 2)
	assert.Equal(t, model.ContentPartTypeText, restored.Parts[0].Type)
	assert.Equal(t, "describe this", restored.Parts[0].Text)
	assert.Equal(t, model.ContentPartTypeImageURL, restored.Parts[1].Type)
	assert.Equal(t, "https://example.com/cat.jpg", restored.Parts[1].ImageURL)
	assert.Equal(t, model.ImageDetailAuto, restored.Parts[1].ImageDetail)
}

// TestFromModel_ToModel_NilParts verifies that a nil Parts field round-trips without panic.
func TestFromModel_ToModel_NilParts(t *testing.T) {
	original := model.Message{
		Role:    model.RoleUser,
		Content: "plain text",
	}
	persisted := FromModel(original)
	assert.Empty(t, persisted.Parts)

	restored := persisted.ToModel()
	assert.Equal(t, "plain text", restored.Content)
	assert.Empty(t, restored.Parts)
}
