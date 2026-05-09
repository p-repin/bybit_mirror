<script lang="ts">
  import { app } from "$lib/store.svelte";
  import { fmtNum } from "$lib/utils";
</script>

{#if app.positions.length === 0}
  <div class="rounded-xl border bg-card text-card-foreground shadow-sm p-12 text-center">
    <p class="text-sm text-muted-foreground">Нет открытых позиций</p>
    <p class="text-xs text-muted-foreground mt-1">
      Откройте позицию на Bybit — она появится здесь автоматически
    </p>
  </div>
{:else}
  <div class="rounded-xl border bg-card text-card-foreground shadow-sm">
    <div class="flex flex-col space-y-1.5 p-6 pb-4">
      <h3 class="text-base font-semibold leading-none tracking-tight">Открытые позиции</h3>
      <p class="text-sm text-muted-foreground">
        {app.positions.length}
        {app.positions.length === 1 ? "позиция" : "позиций"} активна
      </p>
    </div>
    <div class="relative w-full overflow-auto">
      <table class="w-full text-sm">
        <thead>
          <tr class="border-b text-muted-foreground">
            <th class="h-10 px-6 text-left align-middle font-medium">Символ</th>
            <th class="h-10 px-4 text-left align-middle font-medium">Сторона</th>
            <th class="h-10 px-4 text-right align-middle font-medium">Размер</th>
            <th class="h-10 px-4 text-right align-middle font-medium">Avg Price</th>
            <th class="h-10 px-4 text-right align-middle font-medium">Mark Price</th>
            <th class="h-10 px-4 text-right align-middle font-medium">Liq. Price</th>
            <th class="h-10 px-4 text-right align-middle font-medium">Margin</th>
            <th class="h-10 px-6 text-right align-middle font-medium">Unrealised PnL</th>
          </tr>
        </thead>
        <tbody>
          {#each app.positions as p (p.category + p.symbol + p.positionIdx)}
            {@const upnl = parseFloat(p.unrealisedPnl)}
            {@const isLong = p.side === "Buy"}
            <tr class="border-b last:border-b-0 transition-colors hover:bg-accent/50">
              <td class="px-6 py-3 font-medium">{p.symbol}</td>
              <td class="px-4 py-3">
                <span
                  class="inline-flex items-center rounded-md px-2 py-0.5 text-xs font-medium {isLong
                    ? 'bg-[color:var(--up)]/15 text-[color:var(--up)]'
                    : 'bg-[color:var(--down)]/15 text-[color:var(--down)]'}"
                >
                  {isLong ? "LONG" : "SHORT"}
                  <span class="ml-1.5 text-muted-foreground font-normal">×{p.leverage}</span>
                </span>
              </td>
              <td class="px-4 py-3 num text-right">{fmtNum(p.size, 4)}</td>
              <td class="px-4 py-3 num text-right">{fmtNum(p.avgPrice, 2)}</td>
              <td class="px-4 py-3 num text-right">{fmtNum(p.markPrice, 2)}</td>
              <td class="px-4 py-3 num text-right text-[color:var(--down)]">
                {fmtNum(p.liqPrice, 2)}
              </td>
              <td class="px-4 py-3 num text-right">{fmtNum(p.positionValue, 2)}</td>
              <td
                class="px-6 py-3 num text-right font-medium {upnl > 0
                  ? 'text-[color:var(--up)]'
                  : upnl < 0
                    ? 'text-[color:var(--down)]'
                    : 'text-muted-foreground'}"
              >
                {upnl > 0 ? "+" : ""}{fmtNum(upnl, 4)}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  </div>
{/if}
