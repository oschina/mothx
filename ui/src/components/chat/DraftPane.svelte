<script>
  import ChatPane from './ChatPane.svelte';

  // A draft tab reuses the full ChatPane composer. ChatPane allocates a server
  // session ID on the first send and notifies us so the workbench can replace
  // this draft tab with the canonical session tab.
  export let draftId = '';
  export let active = true;
  export let workDir = '';
  export let viewMode = 'chat';
  export let onModeChange = null;
  export let onSessionAllocated = null;
</script>

<ChatPane
  sessionId=""
  draftKey={draftId}
  {active}
  {viewMode}
  {onModeChange}
  initialWorkDir={workDir}
  onFocusSession={onSessionAllocated}
  onSessionAllocated={onSessionAllocated}
/>
