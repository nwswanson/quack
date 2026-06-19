package server

import (
	"quack/internal/domain"
	"quack/internal/storage"
)

type Storage = storage.Storage
type StoredFile = storage.StoredFile
type StoredFileResult = storage.StoredFileResult
type BlobStorage = storage.BlobStorage

func NewBlobStorage(root string) (*BlobStorage, error) {
	return storage.NewBlobStorage(root)
}

type UploadRecord = domain.UploadRecord
type UploadState = domain.UploadState
type UploadFileRecord = domain.UploadFileRecord

const (
	UploadStateUploading = domain.UploadStateUploading
	UploadStateFinished  = domain.UploadStateFinished
	UploadStateError     = domain.UploadStateError
)
