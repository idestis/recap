package risk

import "testing"

func TestClassify(t *testing.T) {
	cases := []struct {
		cmd, ctx string
		want     Level
		prod     bool
	}{
		{"kubectl get pods -n checkout", "staging", Safe, false},
		{"kubectl describe pod x -n checkout", "prod-eu", Safe, true},
		{"kubectl set resources deploy/api --limits=memory=1Gi", "prod-eu", Risky, true},
		{"kubectl rollout restart deploy/api", "staging", Risky, false},
		{"kubectl delete pod -l app=payments --context prod-eu", "staging", Destructive, true},
		{"terraform plan", "", Safe, false},
		{"terraform apply -auto-approve", "", Destructive, false},
		{"helm upgrade api ./chart", "", Risky, false},
		{"aws ec2 terminate-instances --instance-ids i-1", "", Destructive, false},
		{"rm -rf /var/lib/data", "", Destructive, false},
		{"ls -la", "prod-eu", Safe, true},
		{"docker ps -a", "", Safe, false},
		{"docker logs 51aa5ed949d0", "", Safe, false},
		{"docker stop localdb", "", Risky, false},
		{"docker compose up -d localdb", "", Risky, false},
		{"docker rm 51aa5ed949d0", "", Destructive, false},
		{"docker volume rm localdb_data", "", Destructive, false},
		{"docker compose down -v", "", Destructive, false},
		{"docker system prune -af", "", Destructive, false},
	}
	for _, c := range cases {
		got := Classify(c.cmd, c.ctx, []string{"prod-eu"})
		if got.Level != c.want || got.Prod != c.prod {
			t.Errorf("%q: got %s prod=%v, want %s prod=%v", c.cmd, got.Level, got.Prod, c.want, c.prod)
		}
	}
}

func TestContextSwitch(t *testing.T) {
	cases := []struct {
		cmd, want string
		ok        bool
	}{
		{"kubectx prod-eu", "prod-eu", true},
		{"kubectx -", "", false},
		{"kubectx", "", false},
		{"kubectl config use-context arn:aws:eks:eu-west-1:1:cluster/prod", "arn:aws:eks:eu-west-1:1:cluster/prod", true},
		{"kubectl get pods --context prod-eu", "", false},
		{"kubens payments", "", false},
	}
	for _, c := range cases {
		got, ok := ContextSwitch(c.cmd)
		if got != c.want || ok != c.ok {
			t.Errorf("%q: got %q %v, want %q %v", c.cmd, got, ok, c.want, c.ok)
		}
	}
}
