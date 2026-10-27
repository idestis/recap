## MODIFIED payments-crashloop
+ variant: Payments crashloop on staging
+ precondition: Check the feature flag is off first
+ precondition: Confirm the on-call lead agreed to the restart
+ verified: staging-eu 2026-04-10

### Variant steps
1. kubectl config use-context — `safe`

   ```sh
   kubectl config use-context {context}
   ```

2. kubectl rollout restart — `risky`

   ```sh
   kubectl rollout restart deploy/{deployment} -n {namespace}
   ```

   Undo: `kubectl rollout undo deploy/{deployment} -n {namespace}`

3. kubectl get pods — `safe`

   ```sh
   kubectl get pods -n {namespace}
   ```

