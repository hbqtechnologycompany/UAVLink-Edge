package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

var (
	SecretFileName = ".drone_secret"
)

// SetSecretFileName sets the filename used for storing the secret
// This is used in Test Mode to avoid overwriting the production secret
func SetSecretFileName(name string) {
	SecretFileName = name
}

// DroneSecret represents the stored secret key data
type DroneSecret struct {
	DroneUUID string    `json:"drone_uuid"`
	SecretKey string    `json:"secret_key"`
	CreatedAt time.Time `json:"created_at"`
}

// getSecretFilePath returns the absolute path to the secret file
func getSecretFilePath() (string, error) {
	// 1. Prioritize generating the secret right next to the executable
	exePath, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(exePath)
		// But during 'go run', the executable is in /tmp, so fallback to getwd in that case
		if dir != "/tmp" && !filepath.HasPrefix(dir, os.TempDir()) {
			return filepath.Join(dir, SecretFileName), nil
		}
	}

	// 2. Fallback to current working directory
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, SecretFileName), nil
}

// LoadSecret loads the secret key from storage
// Returns (uuid, secretKey, error)
func LoadSecret() (string, string, error) {
	filePath, err := getSecretFilePath()
	if err != nil {
		return "", "", err
	}

	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		// Fallback: try current directory if executable dir fails/is different
		filePath = SecretFileName
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			return "", "", fmt.Errorf("secret file not found: %s", filePath)
		}
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", "", fmt.Errorf("failed to read secret file: %w", err)
	}

	var secret DroneSecret
	if err := json.Unmarshal(data, &secret); err != nil {
		return "", "", fmt.Errorf("failed to parse secret file: %w", err)
	}

	if secret.DroneUUID == "" || secret.SecretKey == "" {
		return "", "", fmt.Errorf("invalid secret file: missing uuid or key")
	}

	return secret.DroneUUID, secret.SecretKey, nil
}

// SaveSecret saves the secret key to storage with restricted permissions
func SaveSecret(droneUUID, secretKey string) error {
	filePath, err := getSecretFilePath()
	if err != nil {
		return err
	}

	secret := DroneSecret{
		DroneUUID: droneUUID,
		SecretKey: secretKey,
		CreatedAt: time.Now(),
	}

	data, err := json.MarshalIndent(secret, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal secret data: %w", err)
	}

	// Chỉ chủ file đọc được (0600). `sudo --register` chạy bằng root nhưng service chạy bằng
	// user dịch vụ: khi là root, trao file cho chủ thư mục cài đặt (vd /opt/dronebridge).
	if err := os.WriteFile(filePath, data, 0600); err != nil {
		return fmt.Errorf("failed to write secret file: %w", err)
	}
	_ = os.Chmod(filePath, 0600)
	if os.Geteuid() == 0 {
		if st, err := os.Stat(filepath.Dir(filePath)); err == nil {
			if sys, ok := st.Sys().(*syscall.Stat_t); ok {
				_ = os.Chown(filePath, int(sys.Uid), int(sys.Gid))
			}
		}
	}

	return nil
}

// SecretExists checks if the secret file exists
func SecretExists() bool {
	filePath, err := getSecretFilePath()
	if err != nil {
		return false
	}

	if _, err := os.Stat(filePath); err == nil {
		return true
	}

	// Check current dir as fallback
	if _, err := os.Stat(SecretFileName); err == nil {
		return true
	}

	return false
}

// DeleteSecret deletes the secret file
func DeleteSecret() error {
	filePath, err := getSecretFilePath()
	if err != nil {
		return err
	}

	if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
		return err
	}

	// Also try current dir
	os.Remove(SecretFileName)

	return nil
}
