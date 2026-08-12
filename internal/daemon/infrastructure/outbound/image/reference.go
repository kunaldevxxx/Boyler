package image

import (
	"fmt"
	"path/filepath"
	"strings"
)

type dockerHubReference struct {
	repository  string
	tag         string
	storageName string
}

func parseDockerHubReference(value string) (dockerHubReference, error) {
	canonical, repository, tag, err := canonicalDockerHubReference(value)
	if err != nil {
		return dockerHubReference{}, err
	}
	return dockerHubReference{
		repository:  repository,
		tag:         tag,
		storageName: encodeStorageName(canonical),
	}, nil
}

// StorageName returns an injective, filesystem-safe key for a Docker Hub
// reference. Equivalent spellings such as alpine and alpine:latest share it.
func StorageName(value string) (string, error) {
	canonical, _, _, err := canonicalDockerHubReference(value)
	if err != nil {
		return "", err
	}
	return encodeStorageName(canonical), nil
}

// LegacyStorageName supports images downloaded before injective reference
// encoding was introduced.
func LegacyStorageName(value string) (string, error) {
	value = strings.TrimPrefix(value, "docker.io/")
	if err := validateReferenceText(value); err != nil {
		return "", err
	}
	name := strings.NewReplacer(":", "_", "/", "_").Replace(value)
	if name == "." || name == ".." || filepath.Base(name) != name {
		return "", fmt.Errorf("invalid image name %q", value)
	}
	return name, nil
}

func canonicalDockerHubReference(value string) (canonical, repository, tag string, err error) {
	value = strings.TrimPrefix(value, "docker.io/")
	if err := validateReferenceText(value); err != nil {
		return "", "", "", err
	}
	if strings.Contains(value, "@") {
		return "", "", "", fmt.Errorf("digest image references are not supported: %q", value)
	}
	lastSlash := strings.LastIndex(value, "/")
	lastColon := strings.LastIndex(value, ":")
	repository = value
	tag = "latest"
	if lastColon > lastSlash {
		repository = value[:lastColon]
		tag = value[lastColon+1:]
	}
	if repository == "" || tag == "" {
		return "", "", "", fmt.Errorf("invalid image reference %q", value)
	}
	for _, component := range strings.Split(repository, "/") {
		if component == "" || component == "." || component == ".." {
			return "", "", "", fmt.Errorf("invalid image reference %q", value)
		}
	}
	canonical = repository + ":" + tag
	if !strings.Contains(repository, "/") {
		repository = "library/" + repository
	}
	return canonical, repository, tag, nil
}

func validateReferenceText(value string) error {
	if value == "" || strings.ContainsAny(value, " \\\x00") {
		return fmt.Errorf("invalid image reference %q", value)
	}
	return nil
}

func encodeStorageName(value string) string {
	const hex = "0123456789ABCDEF"
	var builder strings.Builder
	for index := 0; index < len(value); index++ {
		char := value[index]
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("-._", rune(char)) {
			builder.WriteByte(char)
			continue
		}
		builder.WriteByte('%')
		builder.WriteByte(hex[char>>4])
		builder.WriteByte(hex[char&15])
	}
	return builder.String()
}
