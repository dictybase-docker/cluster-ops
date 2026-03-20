# ArangoDB Post-Restore Password Reset Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** After `restore-local-arangodb` restores a production dump (which overwrites `_system._users` with prod credentials), automatically reset the root password to the local value stored in the `arangodb-root` k8s secret — without ever needing to know the production root password.

**Architecture:** The ArangoDB Kubernetes operator creates a JWT signing secret (`arangodb-jwt` in the `dev` namespace). This JWT secret acts as a superuser credential that bypasses the user password database entirely. Post-restore, we write the JWT to a temp file and pass it to `arangosh` (via Docker) using `--server.jwt-secret-keyfile`, then call `require('@arangodb/users').update('root', newPass)`. The new password is read directly from the `arangodb-root` k8s secret, which already holds the desired local root password.

**Tech Stack:** bash, kubectl, Docker (`arangodb/arangodb:3.11.6`), just

---

## Background / Root Cause

`arangorestore --include-system-collections --all-databases` restores the `_system` database verbatim, including the `_users` collection. This overwrites the local root user's password hash with the production hash. After restore, the local root password is unknown.

**Key facts from codebase:**
- ArangoDeployment CR name: `"arangodb"` (`arangodb-single/main.go:92`)
- Operator-created JWT secret name: `arangodb-jwt` (operator naming convention: `<deployment-name>-jwt`)
- Root password k8s secret: `arangodb-root`, key: `password`
- The recipe already port-forwards to `LOCAL_PORT=9529`; the same port-forward can be reused for the password reset step

---

## Pre-Implementation Verification (Manual — Do First)

Before writing any code, confirm the JWT secret exists and its structure in your running k3d cluster:

```bash
# Export kubeconfig for local cluster
KUBECONFIG=$(k3d kubeconfig get k3d-dev-cluster)

# Verify the JWT secret exists and list its keys
KUBECONFIG="$KUBECONFIG" kubectl get secret arangodb-jwt -n dev -o jsonpath='{.data}' | python3 -m json.tool
```

**Expected output:** a JSON object with a key named `token` (standard ArangoDB operator convention).

If the key name differs from `token`, update the plan accordingly before proceeding.

---

## File Map

| File | Action | Change |
|------|--------|--------|
| `just_modules/arangodb.justfile` | Modify | Add pre-restore password read + post-restore JWT password reset to `restore-local-arangodb` |

No new files needed.

---

## Task 1: Read Desired Root Password Before Restore

**File:** `just_modules/arangodb.justfile`, `restore-local-arangodb` recipe (~line 188)

**Context:** The recipe currently declares `ROOT_PASS="{{ root_pass }}"` from a parameter defaulting to `""`. We need to also capture the desired NEW password from the k8s secret — this must happen after `export KUBECONFIG` (line 202) so kubectl can reach the cluster.

- [ ] **Step 1: Locate insertion point**

  In `restore-local-arangodb`, find the line:
  ```bash
  echo "Discovering ArangoDB service in namespace '{{ namespace }}'..."
  ```
  The new block goes **just before** this line.

- [ ] **Step 2: Add the password read block**

  Insert the following after `export KUBECONFIG="$KUBECONFIG_FILE"` and before the service discovery block:

  ```bash
  echo "Reading desired root password from 'arangodb-root' secret..."
  NEW_ROOT_PASS=$(kubectl get secret arangodb-root -n {{ namespace }} \
      -o jsonpath='{.data.password}' | base64 -d)
  if [[ -z "$NEW_ROOT_PASS" ]]; then
      echo "Error: 'arangodb-root' secret has an empty password field."
      exit 1
  fi
  ```

- [ ] **Step 3: Add JWT temp file to cleanup**

  Find the `cleanup()` function in the recipe and add the JWT temp file variable to it:

  ```bash
  cleanup() {
      if [[ -n "${PF_PID:-}" ]]; then
          echo "Stopping port-forward (PID: $PF_PID)..."
          kill "$PF_PID" || true
      fi
      rm -f "$KUBECONFIG_FILE" "${JWT_FILE:-}"
  }
  ```

  Also declare `JWT_FILE=""` near the top of the recipe (after `ROOT_PASS`) so cleanup is always safe:

  ```bash
  JWT_FILE=""
  ```

---

## Task 2: Add Post-Restore JWT Password Reset

**File:** `just_modules/arangodb.justfile`, `restore-local-arangodb` recipe — after the `docker run arangorestore` block and `echo "Restore complete."`.

- [ ] **Step 1: Locate insertion point**

  Find:
  ```bash
  echo "Restore complete."
  ```
  The new block goes **immediately after** this line.

- [ ] **Step 2: Add the password reset block**

  ```bash
  echo "Fetching operator JWT secret for superuser access..."
  JWT_FILE=$(mktemp -t arangodb-jwt)
  kubectl get secret arangodb-jwt -n {{ namespace }} \
      -o jsonpath='{.data.token}' | base64 -d > "$JWT_FILE"

  echo "Resetting root password to local value from 'arangodb-root' secret..."
  docker run --rm \
      -v "${JWT_FILE}:/jwt.secret:ro" \
      -e "ARANGO_NEW_PASS=${NEW_ROOT_PASS}" \
      arangodb/arangodb:{{ image_tag }} \
      arangosh \
      --server.endpoint "$ENDPOINT" \
      --server.jwt-secret-keyfile /jwt.secret \
      --javascript.execute-string \
          "require('@arangodb/users').update('root', process.env.ARANGO_NEW_PASS); print('Root password reset successfully.');"

  echo "Root password has been reset to the local value."
  ```

  **Why `process.env.ARANGO_NEW_PASS`?** Injecting the password via an environment variable (rather than shell-interpolating it into the JS string) handles passwords with special characters (quotes, backslashes, etc.) safely.

  **Note on `$ENDPOINT`:** This variable is already set earlier in the recipe (either `tcp://host.docker.internal:${LOCAL_PORT}` on macOS or `tcp://127.0.0.1:${LOCAL_PORT}` on Linux). The same port-forward is still active at this point.

---

## Task 3: Manual Integration Test

There is no unit-testable surface for justfile recipes — validate by running the full flow against a live k3d cluster.

- [ ] **Step 1: Ensure k3d cluster is running with ArangoDB deployed**

  ```bash
  just k3d start k3d-dev-cluster
  kubectl get pod -n dev -l app.arangodb.com/deployment-name=arangodb
  # Expected: pod in Running state
  ```

- [ ] **Step 2: Run the restore recipe**

  ```bash
  just arangodb restore-local-arangodb scratch/arangodump
  ```

  Watch for these new log lines to confirm the new steps execute:
  ```
  Reading desired root password from 'arangodb-root' secret...
  Fetching operator JWT secret for superuser access...
  Resetting root password to local value from 'arangodb-root' secret...
  Root password reset successfully.
  Root password has been reset to the local value.
  ```

- [ ] **Step 3: Verify the password works**

  Get the expected password and test connectivity:

  ```bash
  KUBECONFIG=$(k3d kubeconfig get k3d-dev-cluster)
  EXPECTED_PASS=$(KUBECONFIG="$KUBECONFIG" kubectl get secret arangodb-root -n dev \
      -o jsonpath='{.data.password}' | base64 -d)

  docker run --rm arangodb/arangodb:3.11.6 \
      arangosh \
      --server.endpoint tcp://host.docker.internal:9529 \
      --server.username root \
      --server.password "$EXPECTED_PASS" \
      --javascript.execute-string "print(db._databases());"
  ```

  **Expected:** prints a list of databases without authentication error.

- [ ] **Step 4: Commit**

  ```bash
  git add just_modules/arangodb.justfile
  # Then use: /ai-commit-gen:commit
  ```

---

## Failure Modes & Fallbacks

| Failure | Likely cause | Fix |
|---------|-------------|-----|
| `secret "arangodb-jwt" not found` | ArangoDeployment CR is named differently | Run `kubectl get secret -n dev \| grep jwt` to find the actual name, update the recipe |
| arangosh exits with `HTTP 401` | JWT secret key is not `token` | Run the pre-implementation verification step; find the correct key name |
| arangosh exits with `HTTP 401` (different error) | JWT in secret is not a signing key but a signed JWT | Try `--server.authentication-jwt-secret` instead of `--server.jwt-secret-keyfile` |
| `process.env.ARANGO_NEW_PASS` is undefined in arangosh | Old arangosh version doesn't expose env | Use `--javascript.execute-string "require('@arangodb/users').update('root', '$(printf '%s' "$NEW_ROOT_PASS" | sed "s/'/\\\\'/g")');"` instead |
