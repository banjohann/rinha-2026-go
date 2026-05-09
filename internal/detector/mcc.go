package detector

import (
	"encoding/json"
	"os"
)

const DefaultMCCRisk float32 = 0.5

type MCCRisk map[string]float32

func LoadMCCRisk(path string) (MCCRisk, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m := make(MCCRisk)
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func (m MCCRisk) Get(code string) float32 {
	if v, ok := m[code]; ok {
		return v
	}
	return DefaultMCCRisk
}
