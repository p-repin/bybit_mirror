import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

export function fmtNum(v: string | number, digits = 2): string {
  const n = typeof v === "string" ? parseFloat(v) : v;
  if (!isFinite(n)) return "—";
  return n.toLocaleString("en-US", {
    minimumFractionDigits: digits,
    maximumFractionDigits: digits,
  });
}

export function fmtSigned(v: string | number, digits = 4): string {
  const n = typeof v === "string" ? parseFloat(v) : v;
  if (!isFinite(n) || n === 0) return fmtNum(0, digits);
  const sign = n > 0 ? "+" : "";
  return sign + fmtNum(n, digits);
}
