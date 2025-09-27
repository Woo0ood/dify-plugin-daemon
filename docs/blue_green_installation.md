# Blue-Green Plugin Installation Overview

## Existing Installation Flow

- Tenant-triggered installations originate from `InstallPluginRuntimeToTenant` in `internal/service/install_plugin.go`. This workflow
  validates declarations, persists install tasks, and delegates actual runtime creation via `doInstallPluginRuntime`.
- For local runtimes, `doInstallPluginRuntime` invokes `PluginManager.InstallToLocal`, which persists the uploaded package into the
  installed bucket and immediately calls `launchLocal`. The launcher extracts plugin files, prepares the execution environment, and
  registers the runtime with the manager.
- Remote (debugging) runtimes are registered by the TCP watcher inside `internal/core/plugin_manager/watcher.go` once a remote plugin
  connects. Serverless deployments are handled by `InstallToServerlessFromPkg` and related helpers, which upload the package and
  update serverless runtime metadata.

## Existing Invocation Flow

- Service entrypoints use `createSession` (`internal/service/session.go`) to build a `Session` structure, bind plugin metadata, and
  acquire a runtime instance. Streaming helpers such as `GenericInvokePlugin` send messages over the session to the runtime and
  forward responses back to the caller.
- HTTP endpoint invocations follow a similar path in `internal/service/endpoint.go`, performing additional request preparation before
  delegating to the plugin daemon.

## Blue-Green Upgrade Plan

1. **Runtime Tracking** – introduce per-runtime traffic bookkeeping inside `PluginManager` so we know how many active sessions rely on
   each plugin version.
2. **Session Lifecycle Hooks** – ensure every request acquires a runtime through the manager and releases it when the session closes,
   allowing us to decrement the active counter reliably.
3. **Version Handover** – when a new plugin version is launched, mark older versions with the same `plugin_id` as "draining". Draining
   runtimes continue serving existing sessions but reject new ones (new traffic receives the updated identifier).
4. **Automated Retirement** – once a draining runtime reaches zero active sessions, stop the process and remove it from the registry,
   completing the blue-green switch without impacting in-flight traffic.

## Implemented Changes

- Added runtime traffic bookkeeping (`runtime_traffic.go`) with counters, draining flags, and helper methods to mark, clean up, and
  acquire runtimes.
- Extended session management to bind release callbacks so every invocation decrements counters on completion.
- Updated installation and watcher flows to register new runtimes and retire previous versions automatically.
- Adjusted service entrypoints to use the new acquire/release mechanism, guaranteeing smooth blue-green transitions for plugin
  upgrades.
- The plugin installation API accepts a `blue_green` flag so operators can opt into the draining behaviour on a per-request basis
  while preserving the previous immediate-switch semantics by default.
- Added `/plugin/:tenant_id/management/runtime/connections` to expose live connection counts (including split old/new figures
  during a blue-green rollout).
