package scenario

import (
	"fmt"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
	"os"
)

type Scenario struct {
	Name      string   `yaml:"name"`
	ModelName string   `yaml:"model_name"`
	UserID    string   `yaml:"user_id"`
	Messages  []string `yaml:"messages"`
}

// LoadScenario loads a Scenario from a YAML file and applies defaults/validation.
func LoadScenario(path string) (*Scenario, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var scen Scenario
	if err := yaml.NewDecoder(f).Decode(&scen); err != nil {
		return nil, err
	}
	if err := scen.setDefaultsAndValidate(path); err != nil {
		return nil, err
	}
	log.Printf("Loaded '%s' scenario from %s", scen.Name, path)
	return &scen, nil
}

// setDefaultsAndValidate sets default values and validates the scenario.
func (s *Scenario) setDefaultsAndValidate(filename string) error {
	if s.Name == "" {
		s.Name = filename
	}
	if s.ModelName == "" {
		s.ModelName = "Qwen/Qwen1.5-0.5B-Chat"
	}
	if s.UserID == "" {
		s.UserID = "user123"
	}
	if len(s.Messages) == 0 {
		return fmt.Errorf("no messages defined in scenario")
	}
	return nil
}
