package keyboard

type clipboardMetadata struct {
	name  string
	value uint32
}

func clipboardPrivacyFormats() []clipboardMetadata {
	return []clipboardMetadata{
		{name: "ExcludeClipboardContentFromMonitorProcessing", value: 1},
		{name: "CanIncludeInClipboardHistory", value: 0},
		{name: "CanUploadToCloudClipboard", value: 0},
	}
}
