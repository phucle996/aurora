package policyAdapter

import (
	"context"
	"fmt"
	"os"

	policyEntity "controlplane/internal/policyengine/runtime/types"
)

// YAMLFileSourceAdapter reads YAML policy file from fixed runtime path.
// Adapter only handles source read/metadata; validation belongs to service layer.
type YAMLFileSourceAdapter struct {
	PolicyFilePath string
}

func NewYAMLFileSourceAdapter(policyFilePath string) *YAMLFileSourceAdapter {
	return &YAMLFileSourceAdapter{PolicyFilePath: policyFilePath}
}

func (a *YAMLFileSourceAdapter) ReadMeta(_ context.Context) (policyEntity.PolicySourceMeta, error) {
	fileInfo, err := os.Stat(a.PolicyFilePath)
	if err != nil {
		return policyEntity.PolicySourceMeta{}, err
	}
	return policyEntity.PolicySourceMeta{
		Path:    a.PolicyFilePath,
		Version: fmt.Sprintf("%d", fileInfo.ModTime().UnixNano()),
		Size:    fileInfo.Size(),
	}, nil
}

func (a *YAMLFileSourceAdapter) ReadCurrent(_ context.Context) ([]byte, policyEntity.PolicySourceMeta, error) {
	meta, err := a.ReadMeta(context.Background())
	if err != nil {
		return nil, policyEntity.PolicySourceMeta{}, err
	}
	rawBytes, err := os.ReadFile(a.PolicyFilePath)
	if err != nil {
		return nil, policyEntity.PolicySourceMeta{}, err
	}
	return rawBytes, meta, nil
}
