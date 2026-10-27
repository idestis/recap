## MODIFIED payments-crashloop
+ variant: Payments crashloop after config change
+ precondition: Confirm the on-call lead agreed to the restart
+ verified: prod-eu 2026-05-02

### Variant steps
1. kubectx {context} — `safe`

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

7. docker logs {container} — `safe`

   ```sh
   docker logs {container}
   ```

