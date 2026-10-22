package keyboard

import (
	"reflect"
	"testing"
)

func TestClipboardPrivacyFormats(t *testing.T) {
	want := []clipboardMetadata{
		{name: "ExcludeClipboardContentFromMonitorProcessing", value: 1},
		{name: "CanIncludeInClipboardHistory", value: 0},
		{name: "CanUploadToCloudClipboard", value: 0},
	}
	if got := clipboardPrivacyFormats(); !reflect.DeepEqual(got, want) {
		t.Fatalf("clipboard privacy formats = %v, want %v", got, want)
	}
}
