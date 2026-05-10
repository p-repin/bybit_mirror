<script lang="ts">
  import { app } from "$lib/store.svelte";
  import { fmtNum } from "$lib/utils";

  const upl = $derived(app.wallet ? parseFloat(app.wallet.totalPerpUPL) : 0);
  const uplClass = $derived(
    upl > 0 ? "text-[color:var(--up)]" : upl < 0 ? "text-[color:var(--down)]" : "",
  );
  const uplSign = $derived(upl > 0 ? "+" : "");
</script>

{#if app.wallet}
  <div class="rounded-xl border bg-card text-card-foreground shadow-sm p-5 sm:p-6">
    <div class="flex items-start justify-between gap-4">
      <div>
        <div class="text-xs sm:text-sm text-muted-foreground">Total Equity</div>
        <div class="num text-2xl sm:text-3xl font-semibold mt-1 tracking-tight">
          {fmtNum(app.wallet.totalEquity)}
        </div>
      </div>
      <div class="text-right">
        <div class="text-xs sm:text-sm text-muted-foreground">Unrealised PnL</div>
        <div class="num text-2xl sm:text-3xl font-semibold mt-1 tracking-tight {uplClass}">
          {uplSign}{fmtNum(upl)}
        </div>
      </div>
    </div>
    <div
      class="mt-4 flex flex-wrap gap-x-6 gap-y-1 text-xs sm:text-sm text-muted-foreground"
    >
      <div>
        Wallet Balance
        <span class="num text-foreground ml-1.5">
          {fmtNum(app.wallet.totalWalletBalance)}
        </span>
      </div>
      <div>
        Available
        <span class="num text-foreground ml-1.5">
          {fmtNum(app.wallet.totalAvailableBalance)}
        </span>
      </div>
    </div>
  </div>
{/if}
