package ctxdetect

import "testing"

func TestDetectMixed(t *testing.T) {
	items := Detect("rollback ruled out, migration 0142 already ran https://grafana.acme.io/d/checkout")
	if len(items) != 2 {
		t.Fatalf("want note + link, got %+v", items)
	}
	if items[0].Kind != "note" || !items[0].Decision {
		t.Errorf("want decision note, got %+v", items[0])
	}
	if items[1].LinkType != "dashboard" {
		t.Errorf("want dashboard, got %+v", items[1])
	}
}

func TestDetectSlackChannel(t *testing.T) {
	items := Detect("https://acme.slack.com/archives/C07ABC123")
	if len(items) != 1 || items[0].LinkType != "slack" || items[0].Ref != "C07ABC123" {
		t.Fatalf("got %+v", items)
	}
}

func TestDetectPR(t *testing.T) {
	items := Detect("https://github.com/acme/helm-charts/pull/311")
	if items[0].LinkType != "pr" || items[0].Ref != "311" {
		t.Fatalf("got %+v", items)
	}
}
