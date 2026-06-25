package hardware

import (
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

func LoadConfigFile(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, err
	}
	defer file.Close()
	return ParseConfig(file)
}

func ParseConfig(r io.Reader) (Config, error) {
	var config Config
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	if err := dec.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("invalid hardware config: %w", err)
	}
	return config, nil
}
