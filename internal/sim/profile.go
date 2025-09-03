package sim

// Profile captures device and router identity and defaults used by the simulator.
type Profile struct {
	// Identity
	DeviceID string
	SKU      string
	Country  string

	// Dish
	DishHW string
	DishSW string
	Lat    float32
	Lon    float32

	// Router / WiFi
	RouterHW string
	RouterSW string
	SSID     string
	WiFiPass string
}

// DefaultProfile returns a sensible default simulator profile.
func DefaultProfile() Profile {
	return Profile{
		DeviceID: "ut-0000000000000000",
		SKU:      "UT2",
		Country:  "US",

		DishHW: "Dish-Gen2",
		DishSW: "v0.0.0-sim",
		Lat:    47.6205,
		Lon:    -122.3493,

		RouterHW: "Router-Gen2",
		RouterSW: "v0.0.0-sim",
		SSID:     "Starlink-Sim",
		WiFiPass: "starlink123",
	}
}
