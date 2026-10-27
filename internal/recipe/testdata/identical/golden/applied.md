---
id: payments-crashloop
title: Payments crashloop after config change
risk: risky-prod
tools: [curl, docker, helm, kubectl, kubectx]
trust: { runs: 2, ok: 2 }
verified: [prod-eu 2026-05-02, prod-eu 2026-03-04]
vars: [container, context, deployment, namespace, pod]
---

# Payments crashloop after config change

## Situation

payments crashloop after config change. Pods were crash-looping after the config change

## Preconditions

- Confirm the on-call lead agreed to the restart

## Steps

1. kubectx prod-eu — `safe`

   ```sh
   kubectx {context}
   ```

2. kubectl get pods — `safe`

   ```sh
   kubectl get pods -n {namespace}
   ```

3. kubectl logs pod — `safe`

   ```sh
   kubectl logs pod {pod} -n {namespace}
   ```

4. kubectl set image — `risky · prod`

   ```sh
   kubectl set image deploy/{deployment} api=registry.example.com/api:1.4.2 -n {namespace}
   ```

   Undo: `kubectl rollout undo deploy/{deployment} -n {namespace}`

5. curl — `safe`

   ```sh
   curl -H 'Authorization: Bearer [REDACTED:github-token]' https://api.example.com/health
   ```

6. helm upgrade payments — `risky · prod`

   ```sh
   helm upgrade payments ./chart
   ```

   Undo: `helm rollback payments`

7. docker logs 0123456789ab — `safe`

   ```sh
   docker logs {container}
   ```
