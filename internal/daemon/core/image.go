package core

import "time"

type Image struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Tag          string    `json:"tag"`
	Reference    string    `json:"reference"`
	Digest       string    `json:"digest"`
	RootfsDigest string    `json:"rootfsDigest"`
	Size         int64     `json:"size"`
	CreatedAt    time.Time `json:"createdAt"`
	RootfsPath   string    `json:"rootfsPath,omitempty"`
	TarPath      string    `json:"tarPath,omitempty"`
	Layers       []string  `json:"layers,omitempty"`
}

type ImageUsage struct {
	ManifestDigests map[string]struct{}
}

type ImagePruneOptions struct {
	All    bool
	DryRun bool
}

type ImageRemoveResult struct {
	Reference string
	Digest    string
}

type ImagePruneResult struct {
	DeletedReferences     []string
	DeletedManifests      []string
	DeletedRootfs         []string
	DeletedLayers         []string
	QuarantinedReferences []string
	ReclaimedBytes        int64
}
