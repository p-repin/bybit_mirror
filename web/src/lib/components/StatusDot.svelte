<script lang="ts">
  let { connected }: { connected: boolean } = $props();
</script>

<span class="relative flex h-2 w-2">
  {#if connected}
    <span
      class="absolute inline-flex h-full w-full animate-ping rounded-full bg-[color:var(--up)] opacity-50"
    ></span>
  {/if}
  <span
    class="relative inline-flex rounded-full h-2 w-2 {connected
      ? 'bg-[color:var(--up)]'
      : 'bg-[color:var(--down)]'}"
  ></span>
</span>
