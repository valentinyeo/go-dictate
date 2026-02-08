package vosk

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// ModelInfo describes a downloadable Vosk model.
type ModelInfo struct {
	Key      string
	Name     string
	URL      string
	Size     string
	Language string
	DirName  string
}

// Models is the registry of available Vosk models.
var Models = map[string]ModelInfo{
	"en-small": {
		Key:      "en-small",
		Name:     "English Small",
		URL:      "https://alphacephei.com/vosk/models/vosk-model-small-en-us-0.15.zip",
		Size:     "40 MB",
		Language: "en",
		DirName:  "vosk-model-small-en-us-0.15",
	},
	"de-small": {
		Key:      "de-small",
		Name:     "German Small",
		URL:      "https://alphacephei.com/vosk/models/vosk-model-small-de-0.15.zip",
		Size:     "45 MB",
		Language: "de",
		DirName:  "vosk-model-small-de-0.15",
	},
	"en-large": {
		Key:      "en-large",
		Name:     "English Large",
		URL:      "https://alphacephei.com/vosk/models/vosk-model-en-us-0.22.zip",
		Size:     "1.8 GB",
		Language: "en",
		DirName:  "vosk-model-en-us-0.22",
	},
	"de-large": {
		Key:      "de-large",
		Name:     "German Large",
		URL:      "https://alphacephei.com/vosk/models/vosk-model-de-0.21.zip",
		Size:     "1.9 GB",
		Language: "de",
		DirName:  "vosk-model-de-0.21",
	},
}

const voskDLLURL = "https://github.com/alphacep/vosk-api/releases/download/v0.3.45/vosk-win64-0.3.45.zip"

// GetModelsDir returns the path to the models directory.
func GetModelsDir() (string, error) {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return "", fmt.Errorf("APPDATA not set")
	}
	return filepath.Join(appData, "dictation", "models"), nil
}

// GetModelPath returns the path to a specific model's directory.
func GetModelPath(modelKey string) (string, error) {
	info, ok := Models[modelKey]
	if !ok {
		return "", fmt.Errorf("unknown model: %s", modelKey)
	}
	modelsDir, err := GetModelsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(modelsDir, info.DirName), nil
}

// IsModelDownloaded checks if a model has been downloaded.
func IsModelDownloaded(modelKey string) bool {
	path, err := GetModelPath(modelKey)
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// ModelKeyForLanguage returns the model key for the given language and size.
func ModelKeyForLanguage(language string, large bool) string {
	size := "small"
	if large {
		size = "large"
	}
	lang := "en"
	if language == "de" {
		lang = "de"
	}
	return lang + "-" + size
}

// DownloadModel downloads and extracts a Vosk model.
func DownloadModel(modelKey string, progressFn func(downloaded, total int64)) error {
	info, ok := Models[modelKey]
	if !ok {
		return fmt.Errorf("unknown model: %s", modelKey)
	}

	modelsDir, err := GetModelsDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(modelsDir, 0755); err != nil {
		return fmt.Errorf("failed to create models dir: %w", err)
	}

	tmpFile, err := os.CreateTemp(modelsDir, "model-*.zip")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if err := downloadFile(info.URL, tmpFile, progressFn); err != nil {
		tmpFile.Close()
		return err
	}
	tmpFile.Close()

	return extractZip(tmpPath, modelsDir)
}

// EnsureVoskLibrary downloads the Vosk DLL if not already present.
func EnsureVoskLibrary(progressFn func(downloaded, total int64)) error {
	if IsAvailable() {
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get exe path: %w", err)
	}
	exeDir := filepath.Dir(exe)

	tmpFile, err := os.CreateTemp(exeDir, "vosk-lib-*.zip")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if err := downloadFile(voskDLLURL, tmpFile, progressFn); err != nil {
		tmpFile.Close()
		return err
	}
	tmpFile.Close()

	if err := extractDLLsFromZip(tmpPath, exeDir); err != nil {
		return err
	}

	ResetDLL()
	return nil
}

// DeleteModel removes a downloaded model.
func DeleteModel(modelKey string) error {
	path, err := GetModelPath(modelKey)
	if err != nil {
		return err
	}
	return os.RemoveAll(path)
}

func downloadFile(url string, dest *os.File, progressFn func(downloaded, total int64)) error {
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	total := resp.ContentLength
	var downloaded int64
	buf := make([]byte, 64*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := dest.Write(buf[:n]); writeErr != nil {
				return fmt.Errorf("write error: %w", writeErr)
			}
			downloaded += int64(n)
			if progressFn != nil {
				progressFn(downloaded, total)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("download error: %w", readErr)
		}
	}
	return nil
}

func extractZip(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("failed to open zip: %w", err)
	}
	defer r.Close()

	for _, f := range r.File {
		fpath := filepath.Join(destDir, f.Name)

		if !strings.HasPrefix(filepath.Clean(fpath), filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("illegal file path: %s", fpath)
		}

		if f.FileInfo().IsDir() {
			os.MkdirAll(fpath, 0755)
			continue
		}

		if err := os.MkdirAll(filepath.Dir(fpath), 0755); err != nil {
			return err
		}

		if err := extractSingleFile(f, fpath); err != nil {
			return err
		}
	}
	return nil
}

func extractDLLsFromZip(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("failed to open zip: %w", err)
	}
	defer r.Close()

	for _, f := range r.File {
		name := filepath.Base(f.Name)
		if !strings.HasSuffix(strings.ToLower(name), ".dll") {
			continue
		}
		destPath := filepath.Join(destDir, name)
		if err := extractSingleFile(f, destPath); err != nil {
			return err
		}
	}
	return nil
}

func extractSingleFile(f *zip.File, destPath string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	out, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, rc)
	return err
}
