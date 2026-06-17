package protocol

import "sort"

type FileIdentity struct {
	Path string `json:"path"`
	SHA  string `json:"sha"`
	Size int64  `json:"size,omitempty"`
}

type TransferAction string

const (
	TransferUpload           TransferAction = "upload"
	TransferCopyFromPrevious TransferAction = "copy_from_previous"
	TransferDelete           TransferAction = "delete"
)

type FileTransfer struct {
	Path          string         `json:"path"`
	SHA           string         `json:"sha,omitempty"`
	Size          int64          `json:"size,omitempty"`
	Action        TransferAction `json:"action"`
	PreviousSHA   string         `json:"previous_sha,omitempty"`
	PreviousPath  string         `json:"previous_path,omitempty"`
	PreviousFound bool           `json:"previous_found,omitempty"`
}

type TransferPlan struct {
	Files []FileTransfer `json:"files"`
}

func PlanTransfers(previous []FileIdentity, requested []FileIdentity) TransferPlan {
	previousByPath := make(map[string]FileIdentity, len(previous))
	requestedPaths := make(map[string]struct{}, len(requested))
	for _, file := range previous {
		previousByPath[file.Path] = file
	}

	plan := TransferPlan{Files: make([]FileTransfer, 0, len(requested))}
	for _, file := range requested {
		requestedPaths[file.Path] = struct{}{}
		transfer := FileTransfer{
			Path:   file.Path,
			SHA:    file.SHA,
			Size:   file.Size,
			Action: TransferUpload,
		}
		if old, ok := previousByPath[file.Path]; ok && old.SHA == file.SHA {
			transfer.Action = TransferCopyFromPrevious
			transfer.PreviousSHA = old.SHA
			transfer.PreviousPath = old.Path
			transfer.PreviousFound = true
		}
		plan.Files = append(plan.Files, transfer)
	}

	for _, old := range previous {
		if _, ok := requestedPaths[old.Path]; ok {
			continue
		}
		plan.Files = append(plan.Files, FileTransfer{
			Path:          old.Path,
			SHA:           old.SHA,
			Size:          old.Size,
			Action:        TransferDelete,
			PreviousSHA:   old.SHA,
			PreviousPath:  old.Path,
			PreviousFound: true,
		})
	}

	sort.SliceStable(plan.Files, func(i, j int) bool {
		if plan.Files[i].Path == plan.Files[j].Path {
			return plan.Files[i].Action < plan.Files[j].Action
		}
		return plan.Files[i].Path < plan.Files[j].Path
	})
	return plan
}
