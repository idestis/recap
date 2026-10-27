//go:build calibrate

// Calibration for EmbedMatchThreshold against a real embedding endpoint
// (SPEC D-14). Not part of `task check`. Run with:
//
//	RECAP_CALIBRATE_MODEL=nomic-embed-text go test -tags calibrate -run TestCalibrate -v ./internal/recipe/
//
// RECAP_CALIBRATE_BASE_URL defaults to Ollama (http://localhost:11434/v1).
package recipe

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/idestis/recap/internal/config"
	"github.com/idestis/recap/internal/llm"
)

func mk(id, title, situation string, cmds ...string) *Recipe {
	r := &Recipe{ID: id, Title: title, Situation: situation, Tools: []string{"kubectl"}, Verified: "prod-eu 2026-10-01"}
	for _, c := range cmds {
		r.Steps = append(r.Steps, Step{Title: stepTitle(c), Cmd: c})
	}
	return r
}

// Library recipes and, for each, a draft of the same fix written by someone
// else on another service. All share kubectl and prod, so all pass the gate.
var calLibrary = []*Recipe{
	mk("fix-oomkilled-pods", "Fix OOMKilled pods", "Pods restart with OOMKilled after a traffic spike; memory limit too low.",
		"kubectl get pods -n {namespace}", "kubectl describe pod {pod} -n {namespace}",
		"kubectl set resources deploy/{deployment} -n {namespace} --limits=memory=1Gi", "kubectl rollout status deploy/{deployment} -n {namespace}"),
	mk("crashloop-bad-configmap", "Recover CrashLoopBackOff after a bad config change", "Pods crash on start after a ConfigMap edit with a wrong database URL.",
		"kubectl logs {pod} -n {namespace} --previous", "kubectl rollout undo deploy/{deployment} -n {namespace}",
		"kubectl apply -f configmap.yaml -n {namespace}", "kubectl rollout restart deploy/{deployment} -n {namespace}"),
	mk("fix-imagepullbackoff", "Fix ImagePullBackOff", "New pods cannot pull the image: wrong tag pushed or registry secret expired.",
		"kubectl describe pod {pod} -n {namespace}", "kubectl get secret regcred -n {namespace}",
		"kubectl set image deploy/{deployment} app={image} -n {namespace}"),
	mk("renew-expired-tls-cert", "Renew an expired TLS certificate", "Ingress serves an expired certificate; cert-manager failed to renew.",
		"kubectl get certificate -n {namespace}", "kubectl describe certificaterequest -n {namespace}",
		"kubectl delete secret {secret} -n {namespace}", "kubectl annotate certificate {certificate} cert-manager.io/issue-temporary-certificate=true -n {namespace}"),
	mk("node-disk-pressure", "Clear disk pressure on a node", "A node reports DiskPressure and evicts pods; image cache filled the disk.",
		"kubectl describe node {node}", "kubectl cordon {node}", "kubectl drain {node} --ignore-daemonsets --delete-emptydir-data", "kubectl uncordon {node}"),
	mk("raise-hpa-max-replicas", "Raise HPA max replicas under load", "Latency climbs because the autoscaler is pinned at max replicas.",
		"kubectl get hpa -n {namespace}", "kubectl patch hpa {hpa} -n {namespace} -p '{\"spec\":{\"maxReplicas\":30}}'", "kubectl top pods -n {namespace}"),
}

var calDuplicates = []*Recipe{
	mk("checkout-out-of-memory", "Checkout pods out of memory", "checkout-api containers keep getting killed with exit code 137 under load.",
		"kubectl get pods -n checkout", "kubectl describe pod checkout-api-7f9c -n checkout",
		"kubectl set resources deploy/checkout-api -n checkout --limits=memory=2Gi"),
	mk("payments-crashing-after-config", "Payments crashing after config update", "payments-worker fails at boot since someone changed its ConfigMap.",
		"kubectl logs payments-worker-55d -n payments --previous", "kubectl rollout undo deploy/payments-worker -n payments"),
	mk("search-image-not-found", "Search deploy stuck on image pull", "search-api rollout hangs, pods show ErrImagePull for tag v2.3.1.",
		"kubectl describe pod search-api-6c8 -n search", "kubectl set image deploy/search-api app=registry/search-api:v2.3.0 -n search"),
	mk("api-certificate-expired", "API certificate expired", "Browsers show certificate expired for api.example.com; the Certificate is not Ready.",
		"kubectl get certificate -n edge", "kubectl delete secret api-tls -n edge"),
	mk("node-evicting-pods", "Node evicting pods, disk full", "ip-10-0-3-12 is evicting pods with DiskPressure.",
		"kubectl describe node ip-10-0-3-12", "kubectl drain ip-10-0-3-12 --ignore-daemonsets --delete-emptydir-data", "kubectl uncordon ip-10-0-3-12"),
	mk("orders-autoscaler-limit", "Orders autoscaler at its limit", "orders-api p99 latency high; HPA shows 10/10 replicas.",
		"kubectl get hpa -n orders", "kubectl patch hpa orders-api -n orders -p '{\"spec\":{\"maxReplicas\":25}}'"),
}

// Novel problems: they should match no library recipe.
var calNovel = []*Recipe{
	mk("pod-stuck-terminating", "Pod stuck in Terminating", "A pod hangs in Terminating because of a finalizer.",
		"kubectl get pod {pod} -n {namespace} -o yaml", "kubectl patch pod {pod} -n {namespace} -p '{\"metadata\":{\"finalizers\":null}}'"),
	mk("coredns-failures", "Fix DNS resolution failures", "Services fail to resolve each other; CoreDNS pods are overloaded.",
		"kubectl logs -n kube-system -l k8s-app=kube-dns", "kubectl scale deploy/coredns -n kube-system --replicas=4"),
	mk("pvc-pending", "PVC stuck in Pending", "A StatefulSet cannot start because its PersistentVolumeClaim stays Pending.",
		"kubectl get pvc -n {namespace}", "kubectl describe pvc {pvc} -n {namespace}", "kubectl get storageclass"),
	mk("rotate-db-password", "Rotate a leaked database password", "A database password was posted in a public channel.",
		"kubectl create secret generic db-creds --from-literal=password={password} -n {namespace} --dry-run=client -o yaml", "kubectl rollout restart deploy/{deployment} -n {namespace}"),
}

func draftText(r *Recipe) string {
	return r.Title + " " + r.Situation + " " + strings.Join(r.Tools, " ") + " " + r.stepsMarkdown()
}

func TestCalibrate(t *testing.T) {
	model := os.Getenv("RECAP_CALIBRATE_MODEL")
	if model == "" {
		t.Skip("set RECAP_CALIBRATE_MODEL")
	}
	base := os.Getenv("RECAP_CALIBRATE_BASE_URL")
	if base == "" {
		base = "http://localhost:11434/v1"
	}
	cl, err := llm.New(config.Model{Provider: "ollama", BaseURL: base, Name: model})
	if err != nil {
		t.Fatal(err)
	}
	score := calScores(t, cl, func(r *Recipe) string { return r.Render() }, draftText)
	var pos, neg []float64
	for d := range score {
		var row []string
		for l := range score[d] {
			s := score[d][l]
			if d < len(calDuplicates) && d == l {
				pos = append(pos, s)
				row = append(row, fmt.Sprintf("[%.2f]", s))
			} else {
				neg = append(neg, s)
				row = append(row, fmt.Sprintf(" %.2f ", s))
			}
		}
		name := ""
		if d < len(calDuplicates) {
			name = calDuplicates[d].ID
		} else {
			name = calNovel[d-len(calDuplicates)].ID
		}
		t.Logf("%-32s %s", name, strings.Join(row, ""))
	}
	sort.Float64s(pos)
	sort.Float64s(neg)
	t.Logf("same problem:      min %.3f  median %.3f  max %.3f", pos[0], pos[len(pos)/2], pos[len(pos)-1])
	t.Logf("different problem: min %.3f  median %.3f  max %.3f", neg[0], neg[len(neg)/2], neg[len(neg)-1])
	t.Logf("current threshold: %.2f", EmbedMatchThreshold)

	for _, s := range pos {
		if s < EmbedMatchThreshold {
			t.Errorf("a duplicate scored %.3f, below the threshold", s)
		}
	}
	for _, s := range neg {
		if s >= EmbedMatchThreshold {
			t.Errorf("a different problem scored %.3f, at or above the threshold", s)
		}
	}
}

// calScores embeds the library with lt and the drafts with dt and returns
// draft × library cosine similarities.
func calScores(t *testing.T, cl *llm.Client, lt, dt func(*Recipe) string) [][]float64 {
	var texts []string
	for _, r := range calLibrary {
		texts = append(texts, embedText(lt(r)))
	}
	for _, r := range append(append([]*Recipe{}, calDuplicates...), calNovel...) {
		texts = append(texts, embedText(dt(r)))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	vecs, err := cl.Embed(ctx, texts)
	if err != nil {
		t.Fatal(err)
	}
	lib, drafts := vecs[:len(calLibrary)], vecs[len(calLibrary):]
	out := make([][]float64, len(drafts))
	for d := range drafts {
		for l := range lib {
			out[d] = append(out[d], Cosine(drafts[d], lib[l]))
		}
	}
	return out
}
