package sim

import (
	"errors"
	"io/fs"
	"os"

	yaml "gopkg.in/yaml.v3"
)

// BaseConfig represents the YAML-serializable simulator device config.
type BaseConfig struct {
	// Identity
	DeviceID string `yaml:"device_id"`
	SKU      string `yaml:"sku"`
	Country  string `yaml:"country"`

	// Dish
	DishHW string  `yaml:"dish_hw"`
	DishSW string  `yaml:"dish_sw"`
	Lat    float32 `yaml:"lat"`
	Lon    float32 `yaml:"lon"`

	// Router / WiFi
	RouterHW string `yaml:"router_hw"`
	RouterSW string `yaml:"router_sw"`
	SSID     string `yaml:"ssid"`
	WiFiPass string `yaml:"wifi_pass"`

	EnableRouter bool `yaml:"enable_router"`
	EnableWifi   bool `yaml:"enable_wifi"`
}

// FromProfile converts a Profile to BaseConfig.
func FromProfile(p Profile) BaseConfig {
	return BaseConfig{
		DeviceID:     p.DeviceID,
		SKU:          p.SKU,
		Country:      p.Country,
		DishHW:       p.DishHW,
		DishSW:       p.DishSW,
		Lat:          p.Lat,
		Lon:          p.Lon,
		RouterHW:     p.RouterHW,
		RouterSW:     p.RouterSW,
		SSID:         p.SSID,
		WiFiPass:     p.WiFiPass,
		EnableRouter: p.EnableRouter,
		EnableWifi:   p.EnableWifi,
	}
}

// ToProfile converts BaseConfig to a simulator Profile.
func (c BaseConfig) ToProfile() Profile {
	return Profile{
		DeviceID:     c.DeviceID,
		SKU:          c.SKU,
		Country:      c.Country,
		DishHW:       c.DishHW,
		DishSW:       c.DishSW,
		Lat:          c.Lat,
		Lon:          c.Lon,
		RouterHW:     c.RouterHW,
		RouterSW:     c.RouterSW,
		SSID:         c.SSID,
		WiFiPass:     c.WiFiPass,
		EnableRouter: c.EnableRouter,
		EnableWifi:   c.EnableWifi,
	}
}

// WriteTemplateConfig writes a template config based on DefaultProfile to the given path.
// It will not overwrite existing files unless overwrite is true.
func WriteTemplateConfig(path string, perm fs.FileMode, overwrite bool) error {
	if !overwrite {
		if _, err := os.Stat(path); err == nil {
			return nil
		}
	}
	cfg := FromProfile(DefaultProfile())
	// Include toggles explicitly in template
	if cfg.EnableRouter == false {
		cfg.EnableRouter = true
	}
	if cfg.EnableWifi == false {
		cfg.EnableWifi = true
	}
	b, err := yaml.Marshal(&cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, perm)
}

// LoadConfig reads a YAML config file and returns a Profile.
func LoadConfig(path string) (Profile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Profile{}, err
	}
	var cfg BaseConfig
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return Profile{}, err
	}
	// Minimal validation
	if cfg.DeviceID == "" {
		return Profile{}, errors.New("config: device_id is required")
	}
	return cfg.ToProfile(), nil
}
