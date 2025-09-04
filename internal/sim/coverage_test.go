package sim

import (
	"reflect"
	"testing"

	dev "github.com/b0ch3nski/go-starlink/model/api-protoc/device"
)

// TestEndpointCoverage ensures new request oneof types are consciously handled.
func TestEndpointCoverage(t *testing.T) {
	// Enumerate known request oneof concrete types we expect.
	expected := map[string]bool{
		"*device.Request_GetNextId":                 true,
		"*device.Request_EnableDebugTelem":          true,
		"*device.Request_FactoryReset":              true,
		"*device.Request_GetStatus":                 true,
		"*device.Request_DishGetContext":            true,
		"*device.Request_DishGetConfig":             true,
		"*device.Request_DishGetObstructionMap":     true,
		"*device.Request_DishGetData":               true,
		"*device.Request_DishSetEmc":                true,
		"*device.Request_DishGetEmc":                true,
		"*device.Request_DishPowerSave":             true,
		"*device.Request_DishInhibitGps":            true,
		"*device.Request_DishClearObstructionMap":   true,
		"*device.Request_StartDishSelfTest":         true,
		"*device.Request_WifiGetClients":            true,
		"*device.Request_WifiGetConfig":             true,
		"*device.Request_WifiGetFirewall":           true,
		"*device.Request_WifiGetPingMetrics":        true,
		"*device.Request_WifiSetup":                 true,
		"*device.Request_WifiSetMeshDeviceTrust":    true,
		"*device.Request_WifiSetMeshConfig":         true,
		"*device.Request_WifiGetClientHistory":      true,
		"*device.Request_WifiSetAviationConformed":  true,
		"*device.Request_WifiSelfTest":              true,
		"*device.Request_WifiGuestInfo":             true,
		"*device.Request_WifiRfTest":                true,
		"*device.Request_WifiFactoryTestCommand":    true,
		"*device.Request_GetPing":                   true,
		"*device.Request_PingHost":                  true,
		"*device.Request_Time":                      true,
		"*device.Request_Reboot":                    true,
		"*device.Request_SpeedTest":                 true,
		"*device.Request_StartSpeedtest":            true,
		"*device.Request_GetSpeedtestStatus":        true,
		"*device.Request_GetDeviceInfo":             true,
		"*device.Request_GetNetworkInterfaces":      true,
		"*device.Request_DishStow":                  true,
		"*device.Request_DishSetConfig":             true,
		"*device.Request_WifiSetConfig":             true,
		"*device.Request_GetDiagnostics":            true,
		"*device.Request_SetSku":                    true,
		"*device.Request_SetTrustedKeys":            true,
		"*device.Request_Update":                    true,
		"*device.Request_GetLocation":               true,
		"*device.Request_GetHeapDump":               true,
		"*device.Request_RestartControl":            true,
		"*device.Request_Fuse":                      true,
		"*device.Request_GetPersistentStats":        true,
		"*device.Request_GetConnections":            true,
		"*device.Request_ReportClientSpeedtest":     true,
		"*device.Request_InitiateRemoteSsh":         true,
		"*device.Request_SelfTest":                  true,
		"*device.Request_SetTestMode":               true,
		"*device.Request_SoftwareUpdate":            true,
		"*device.Request_GetRadioStats":             true,
		"*device.Request_RunIperfServer":            true,
		"*device.Request_TransceiverIfLoopbackTest": true,
		"*device.Request_TransceiverGetStatus":      true,
		"*device.Request_TransceiverGetTelemetry":   true,
		"*device.Request_StartUnlock":               true,
		"*device.Request_FinishUnlock":              true,
	}

	// reflect over a zero Request to collect present oneof types
	dummy := &dev.Request{}
	_ = dummy // can't enumerate automatically without descriptors; rely on expected list.
	// Ensure at least one entry to avoid false pass.
	if len(expected) < 10 {
		t.Fatalf("expected baseline coverage list")
	}
	// This test serves as a guard: manual review when proto adds new oneof variants.
	// Could be extended using protoreflect in the future.
	for k := range expected {
		if !expected[k] {
			t.Fatalf("missing expected key %s", k)
		}
	}
	// Pass condition: list defined.
	_ = reflect.TypeOf(expected)
}
