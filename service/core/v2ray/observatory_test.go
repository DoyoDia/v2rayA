package v2ray

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/v2rayA/v2rayA/core/coreObj"
	"github.com/v2rayA/v2rayA/core/serverObj"
	"github.com/v2rayA/v2rayA/core/v2ray/where"
	"github.com/v2rayA/v2rayA/db/configure"
)

// fakeServerObj is a minimal serverObj.ServerObj used to exercise observatory
// config generation without touching the database. Only GetName is meaningful.
type fakeServerObj struct{ name string }

func (f *fakeServerObj) Configuration(info serverObj.PriorInfo) (serverObj.Configuration, error) {
	return serverObj.Configuration{}, nil
}
func (f *fakeServerObj) ExportToURL() string  { return "" }
func (f *fakeServerObj) NeedPluginPort() bool { return false }
func (f *fakeServerObj) ProtoToShow() string  { return "" }
func (f *fakeServerObj) GetProtocol() string  { return "vmess" }
func (f *fakeServerObj) GetHostname() string  { return "example.com" }
func (f *fakeServerObj) GetPort() int         { return 443 }
func (f *fakeServerObj) GetName() string      { return f.name }
func (f *fakeServerObj) SetName(name string)  { f.name = name }

// newLeastPingTemplate builds a Template with one leastPing balancer group per entry
// in groups (group tag -> member server names) plus a matching ServerData.
func newLeastPingTemplate(variant where.Variant, groups map[string][]string, probe map[string][2]string) (*Template, *ServerData) {
	t := &Template{Variant: variant}
	sd := &ServerData{
		OutboundName2Setting:    map[string]configure.OutboundSetting{},
		OutboundName2ServerObjs: map[string][]serverObj.ServerObj{},
	}
	for group, members := range groups {
		for _, m := range members {
			t.Outbounds = append(t.Outbounds, coreObj.OutboundObject{
				Tag:       GroupWrapper(m),
				Protocol:  "vmess",
				Balancers: []string{group},
			})
			sd.OutboundName2ServerObjs[group] = append(sd.OutboundName2ServerObjs[group], &fakeServerObj{name: m})
		}
		url, interval := "https://probe.test/204", "60s"
		if p, ok := probe[group]; ok {
			url, interval = p[0], p[1]
		}
		sd.OutboundName2Setting[group] = configure.OutboundSetting{
			ProbeURL:      url,
			ProbeInterval: interval,
			Type:          configure.LeastPing,
		}
	}
	return t, sd
}

func balancerByTag(t *Template, tag string) (coreObj.Balancer, bool) {
	for _, b := range t.Routing.Balancers {
		if b.Tag == tag {
			return b, true
		}
	}
	return coreObj.Balancer{}, false
}

func TestBuildObservatory_XrayCore_SingleGlobalObservatory(t *testing.T) {
	tmpl, sd := newLeastPingTemplate(where.XrayCore,
		map[string][]string{"groupA": {"s1", "s2"}}, nil)

	groupSelectors := tmpl.buildBalancersAndObservatory(sd)

	if tmpl.MultiObservatory != nil {
		t.Fatalf("xray must NOT emit multiObservatory, got %+v", tmpl.MultiObservatory)
	}
	if tmpl.Observatory == nil {
		t.Fatal("xray must emit a single global observatory")
	}
	if tmpl.Observatory.PingConfig != nil {
		t.Errorf("xray observatory should use probeURL/probeInterval, not pingConfig")
	}
	// interval is normalized through time.Duration.String(): "60s" -> "1m0s"
	// (still valid for xray, which parses it back with time.ParseDuration).
	if tmpl.Observatory.ProbeURL != "https://probe.test/204" || tmpl.Observatory.ProbeInterval != "1m0s" {
		t.Errorf("unexpected probe settings: %+v", tmpl.Observatory)
	}
	wantSel := []string{GroupWrapper("s1"), GroupWrapper("s2")}
	gotSel := append([]string(nil), tmpl.Observatory.SubjectSelector...)
	sort.Strings(wantSel)
	sort.Strings(gotSel)
	if strings.Join(gotSel, ",") != strings.Join(wantSel, ",") {
		t.Errorf("subjectSelector = %v, want %v", gotSel, wantSel)
	}
	b, ok := balancerByTag(tmpl, "groupA")
	if !ok {
		t.Fatal("balancer groupA not found")
	}
	if b.Strategy.Settings != nil {
		t.Errorf("xray balancer must not carry strategy.settings (observerTag): %+v", b.Strategy.Settings)
	}
	if got := groupSelectors["groupA"]; len(got) != 2 {
		t.Errorf("groupSelectors[groupA] = %v, want 2 members", got)
	}

	// JSON must contain "observatory" and not "multiObservatory" / "observerTag".
	js, _ := json.Marshal(tmpl)
	s := string(js)
	if !strings.Contains(s, `"observatory"`) {
		t.Errorf("JSON missing observatory: %s", s)
	}
	if strings.Contains(s, "multiObservatory") {
		t.Errorf("JSON must not contain multiObservatory: %s", s)
	}
	if strings.Contains(s, "observerTag") {
		t.Errorf("JSON must not contain observerTag for xray: %s", s)
	}
}

func TestBuildObservatory_XrayCore_MultiGroupSharesObservatory(t *testing.T) {
	tmpl, sd := newLeastPingTemplate(where.XrayCore,
		map[string][]string{
			"groupA": {"s1", "s2"},
			"groupB": {"s2", "s3"}, // s2 shared across groups -> must be deduped
		},
		map[string][2]string{
			"groupA": {"https://a.test/204", "30s"},
			"groupB": {"https://b.test/204", "90s"},
		})

	tmpl.buildBalancersAndObservatory(sd)

	if tmpl.Observatory == nil {
		t.Fatal("expected a single shared observatory")
	}
	// subjectSelector is the deduped union of both groups' members.
	seen := map[string]int{}
	for _, s := range tmpl.Observatory.SubjectSelector {
		seen[s]++
	}
	for _, m := range []string{"s1", "s2", "s3"} {
		if seen[GroupWrapper(m)] != 1 {
			t.Errorf("subjectSelector should contain %q exactly once, got %d (%v)",
				GroupWrapper(m), seen[GroupWrapper(m)], tmpl.Observatory.SubjectSelector)
		}
	}
	if len(tmpl.Routing.Balancers) != 2 {
		t.Errorf("expected 2 balancers, got %d", len(tmpl.Routing.Balancers))
	}
}

func TestBuildObservatory_V2rayaCore_MultiObservatory(t *testing.T) {
	tmpl, sd := newLeastPingTemplate(where.V2rayaCore,
		map[string][]string{"groupA": {"s1", "s2"}}, nil)

	tmpl.buildBalancersAndObservatory(sd)

	if tmpl.Observatory != nil {
		t.Fatalf("v2raya_core must use multiObservatory, not a single observatory: %+v", tmpl.Observatory)
	}
	if tmpl.MultiObservatory == nil || len(tmpl.MultiObservatory.Observers) != 1 {
		t.Fatalf("expected exactly one observer in multiObservatory, got %+v", tmpl.MultiObservatory)
	}
	obs := tmpl.MultiObservatory.Observers[0]
	if obs.Tag != "groupA" {
		t.Errorf("observer tag = %q, want groupA", obs.Tag)
	}
	if obs.Settings.PingConfig == nil {
		t.Errorf("v2raya_core observer should set pingConfig")
	}
	b, ok := balancerByTag(tmpl, "groupA")
	if !ok {
		t.Fatal("balancer groupA not found")
	}
	if b.Strategy.Settings == nil || b.Strategy.Settings.ObserverTag != "groupA" {
		t.Errorf("v2raya_core balancer must bind observerTag=groupA, got %+v", b.Strategy.Settings)
	}

	js, _ := json.Marshal(tmpl)
	if !strings.Contains(string(js), "multiObservatory") {
		t.Errorf("JSON missing multiObservatory: %s", js)
	}
}
