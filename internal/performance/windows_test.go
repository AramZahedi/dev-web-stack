package performance

import "testing"

func TestWindowsHostValuesByProfile(t *testing.T) {
	balanced := computeWindowsHost(testResources(), Balanced)
	if balanced.PowerPlan != powerPlanBalanced {
		t.Fatalf("balanced power plan = %q", balanced.PowerPlan)
	}
	if balanced.DynamicPortMin != 49152 || balanced.DynamicPortMax != 65535 {
		t.Fatalf("balanced ports = %d-%d", balanced.DynamicPortMin, balanced.DynamicPortMax)
	}
	if balanced.NetworkThrottlingIndex != networkThrottlingDefault {
		t.Fatalf("balanced throttling = %d", balanced.NetworkThrottlingIndex)
	}
	if balanced.DisableLastAccess || balanced.Disable8dot3 {
		t.Fatal("balanced profile disabled NTFS features")
	}
	if !balanced.DefenderExclusions {
		t.Fatal("balanced profile did not include Defender exclusions")
	}
	if balanced.WSLMemoryMB != 8192 {
		t.Fatalf("balanced WSL memory = %d, want 8192", balanced.WSLMemoryMB)
	}
	if err := balanced.Validate(); err != nil {
		t.Fatalf("balanced validation: %v", err)
	}

	throughput := computeWindowsHost(testResources(), Throughput)
	if throughput.PowerPlan != powerPlanHighPerformance {
		t.Fatalf("throughput power plan = %q", throughput.PowerPlan)
	}
	if throughput.DynamicPortMin != 32768 || throughput.DynamicPortMax != 60999 {
		t.Fatalf("throughput ports = %d-%d", throughput.DynamicPortMin, throughput.DynamicPortMax)
	}
	if throughput.TCPTimedWaitDelaySeconds != 30 {
		t.Fatalf("throughput timed wait = %d", throughput.TCPTimedWaitDelaySeconds)
	}
	if throughput.NetworkThrottlingIndex != networkThrottlingOff {
		t.Fatalf("throughput throttling = %d", throughput.NetworkThrottlingIndex)
	}
	if !throughput.DisableLastAccess || !throughput.Disable8dot3 || !throughput.DefenderExclusions {
		t.Fatalf("throughput advanced values = %+v", throughput)
	}
	if throughput.WSLMemoryMB != 12288 {
		t.Fatalf("throughput WSL memory = %d, want 12288", throughput.WSLMemoryMB)
	}
	if throughput.WSLProcessors != 8 {
		t.Fatalf("throughput WSL processors = %d, want 8", throughput.WSLProcessors)
	}
	if err := throughput.Validate(); err != nil {
		t.Fatalf("throughput validation: %v", err)
	}

	computed := mustCompute(t, testResources(), Throughput)
	if computed.Windows != throughput {
		t.Fatalf("computed Windows values = %+v, want %+v", computed.Windows, throughput)
	}
}

func TestWindowsCustomDefaultsAndValidation(t *testing.T) {
	res := testResources()
	base := mustCompute(t, res, Throughput)
	base.Profile = Custom
	base.Windows = WindowsHostValues{
		PowerPlan:      powerPlanBalanced,
		DynamicPortMin: 40000,
		DynamicPortMax: 50000,
		WSLMemoryMB:    4096,
		WSLProcessors:  4,
	}
	v, err := Compute(res, Options{Profile: Custom, Custom: &base})
	if err != nil {
		t.Fatalf("Compute(custom): %v", err)
	}
	if v.Windows.PowerPlan != powerPlanBalanced || v.Windows.DynamicPortMin != 40000 || v.Windows.WSLMemoryMB != 4096 {
		t.Fatalf("custom Windows values = %+v", v.Windows)
	}
	if v.Windows.TCPTimedWaitDelaySeconds != 30 || v.Windows.NetworkThrottlingIndex != networkThrottlingOff {
		t.Fatalf("custom defaults not filled: %+v", v.Windows)
	}
	if v.Windows.WSLSwapMB != 0 {
		t.Fatalf("custom explicit swap 0 = %d, want 0", v.Windows.WSLSwapMB)
	}

	bad := WindowsHostValues{PowerPlan: "turbo", DynamicPortMin: 10, DynamicPortMax: 20, WSLMemoryMB: 1, WSLProcessors: 0}
	if err := bad.Validate(); err == nil {
		t.Fatal("invalid Windows values were accepted")
	}
}
