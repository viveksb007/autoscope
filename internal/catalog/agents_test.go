package catalog

import "testing"

func TestLookupKnown(t *testing.T) {
	a := Lookup("kubelet")
	if a.Unit != "kubelet.service" {
		t.Errorf("Unit = %q, want kubelet.service", a.Unit)
	}
	if e, ok := a.FindEndpoint("metrics"); !ok || e.Transport != APIServerProxy {
		t.Errorf("kubelet metrics endpoint missing/non-apiserver: %+v ok=%v", e, ok)
	}
}

func TestLookupByUnitName(t *testing.T) {
	a := Lookup("kubelet.service")
	if a.Unit != "kubelet.service" || a.Alias != "kubelet" {
		t.Errorf("Lookup by unit name lost alias: %+v", a)
	}
}

func TestLookupFallbackPlainAlias(t *testing.T) {
	a := Lookup("custom-svc")
	if a.Unit != "custom-svc.service" {
		t.Errorf("fallback should append .service: %q", a.Unit)
	}
	if len(a.Endpoints) != 0 {
		t.Errorf("fallback should have no endpoints, got %d", len(a.Endpoints))
	}
}

func TestLookupFallbackPreservesDotService(t *testing.T) {
	a := Lookup("my-thing.service")
	if a.Unit != "my-thing.service" {
		t.Errorf("fallback double-suffixed: %q", a.Unit)
	}
}

func TestPodIdentityNoMetrics(t *testing.T) {
	a := Lookup("pod-identity")
	if _, ok := a.FindEndpoint("metrics"); ok {
		t.Errorf("pod-identity should have no metrics endpoint")
	}
	if _, ok := a.FindEndpoint("healthz"); !ok {
		t.Errorf("pod-identity should have healthz endpoint")
	}
}

func TestEndpointNames(t *testing.T) {
	a := Lookup("kubelet")
	names := a.EndpointNames()
	if len(names) != 4 {
		t.Errorf("kubelet should have 4 endpoints, got %v", names)
	}
}
