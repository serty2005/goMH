package iikoplugins

import "testing"

func TestCompareSemanticVersionsPrerelease(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		left, right string
		want        int
	}{
		{left: "1.0.27-beta", right: "1.0.26", want: 1},
		{left: "1.0.27-beta", right: "1.0.28", want: -1},
		{left: "1.0.27-beta", right: "1.0.27", want: -1},
		{left: "1.0.27", right: "1.0.27-rc.1", want: 1},
		{left: "1.0.27-alpha", right: "1.0.27-beta", want: -1},
		{left: "1.0.27-beta", right: "1.0.27-rc", want: -1},
		{left: "1.0.27-beta.2", right: "1.0.27-beta.11", want: -1},
		{left: "1.0.27-beta", right: "1.0.27-beta.1", want: -1},
		{left: "1.0", right: "1.0.0", want: 0},
		{left: "9.4.8049.0", right: "9.4.7039.0", want: 1},
		{left: "", right: "0.0.0", want: 0},
	} {
		t.Run(test.left+" vs "+test.right, func(t *testing.T) {
			t.Parallel()
			if got := compareSemanticVersions(test.left, test.right); got != test.want {
				t.Fatalf("comparison = %d, want %d", got, test.want)
			}
			if got := compareSemanticVersions(test.right, test.left); got != -test.want {
				t.Fatalf("reverse comparison = %d, want %d", got, -test.want)
			}
		})
	}
}

func TestSelectBestPluginVersionPrefersStableRelease(t *testing.T) {
	t.Parallel()

	for _, suffix := range []string{"alpha", "beta", "rc", "beta.2", "RC1"} {
		t.Run(suffix, func(t *testing.T) {
			t.Parallel()
			stable := Plugin{Name: "BeOpen.JoinLeader", ApiVersion: "V7", PluginVersion: "1.0.26"}
			prerelease := Plugin{Name: "BeOpen.JoinLeader", ApiVersion: "V7", PluginVersion: "1.0.27-" + suffix}
			best := selectBestPluginVersion([]Plugin{prerelease, stable})
			if best == nil || *best != stable {
				t.Fatalf("best = %+v, want stable %+v", best, stable)
			}
		})
	}
}

func TestSelectBestPluginVersionRetainsPrereleaseFallback(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		versions []Plugin
		want     Plugin
	}{
		{
			name: "newer beta base version",
			versions: []Plugin{
				{Name: "BeOpen.JoinLeader", ApiVersion: "V7", PluginVersion: "1.0.26-beta"},
				{Name: "BeOpen.JoinLeader", ApiVersion: "V7", PluginVersion: "1.0.27-beta"},
			},
			want: Plugin{Name: "BeOpen.JoinLeader", ApiVersion: "V7", PluginVersion: "1.0.27-beta"},
		},
		{
			name: "rc follows alpha and beta at same base version",
			versions: []Plugin{
				{Name: "BeOpen.JoinLeader", ApiVersion: "V7", PluginVersion: "1.0.27-beta"},
				{Name: "BeOpen.JoinLeader", ApiVersion: "V7", PluginVersion: "1.0.27-rc"},
				{Name: "BeOpen.JoinLeader", ApiVersion: "V7", PluginVersion: "1.0.27-alpha"},
			},
			want: Plugin{Name: "BeOpen.JoinLeader", ApiVersion: "V7", PluginVersion: "1.0.27-rc"},
		},
		{
			name: "API preview fallback still works",
			versions: []Plugin{
				{Name: "Transport", ApiVersion: "V9Preview7", PluginVersion: "9.7.19"},
				{Name: "Transport", ApiVersion: "V9Preview7", PluginVersion: "9.7.20"},
			},
			want: Plugin{Name: "Transport", ApiVersion: "V9Preview7", PluginVersion: "9.7.20"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			best := selectBestPluginVersion(test.versions)
			if best == nil || *best != test.want {
				t.Fatalf("best = %+v, want %+v", best, test.want)
			}
		})
	}
}
