"use client";

import React, { memo, useRef, useEffect, useMemo, useCallback } from "react";
import { useVirtualizer } from "@tanstack/react-virtual";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { useLogStore, LogLevel } from "@/store/logStore";
import { useSSE } from "@/hooks/useSSE";

const BACKEND = process.env.NEXT_PUBLIC_BACKEND_URL ?? "http://localhost:8080";

// highlight patterns that must be surfaced prominently
const HIGH_PRIORITY_PATTERNS = [
  /Payload\s*Mismatch/i,
  /WAF\s*Challenge\s*Triggered/i,
  /challenge/i,
];

function isHighPriority(msg: string) {
  return HIGH_PRIORITY_PATTERNS.some((re) => re.test(msg));
}

const LEVEL_COLORS: Record<LogLevel, string> = {
  INFO: "text-cyan-400",
  DEBUG: "text-slate-400",
  ERROR: "text-red-400",
};

const LEVEL_BG: Record<LogLevel, string> = {
  INFO: "",
  DEBUG: "",
  ERROR: "bg-red-900/20",
};

// ─── Log Terminal ─────────────────────────────────────────────────────────────

export const LogTerminal = memo(function LogTerminal() {
  const addEntry = useLogStore((s) => s.addEntry);
  const entries = useLogStore((s) => s.entries);
  const filters = useLogStore((s) => s.filters);
  const toggleFilter = useLogStore((s) => s.toggleFilter);

  useSSE(`${BACKEND}/api/logs/stream`, (raw) => {
    try {
      const e = JSON.parse(raw);
      addEntry(e);
    } catch {
      // ignore
    }
  });

  const visibleEntries = useMemo(
    () => entries.filter((e) => filters[e.level as LogLevel] ?? true),
    [entries, filters]
  );

  const parentRef = useRef<HTMLDivElement>(null);
  const autoScroll = useRef(true);

  const rowVirtualizer = useVirtualizer({
    count: visibleEntries.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => 22,
    overscan: 20,
  });

  // Auto-scroll to bottom when new entries arrive (if user hasn't scrolled up)
  useEffect(() => {
    if (!autoScroll.current) return;
    rowVirtualizer.scrollToIndex(visibleEntries.length - 1, { align: "end" });
  }, [visibleEntries.length, rowVirtualizer]);

  const handleScroll = useCallback(() => {
    const el = parentRef.current;
    if (!el) return;
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
    autoScroll.current = atBottom;
  }, []);

  const levels: LogLevel[] = ["INFO", "DEBUG", "ERROR"];

  return (
    <section className="space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="text-sm font-semibold uppercase tracking-widest text-slate-400">
          Real-Time Log Terminal
        </h2>
        <div className="flex gap-1">
          {levels.map((lvl) => (
            <Button
              key={lvl}
              size="sm"
              variant={filters[lvl] ? "default" : "outline"}
              onClick={() => toggleFilter(lvl)}
              className={filters[lvl] ? LEVEL_COLORS[lvl] : "text-slate-600"}
            >
              {lvl}
            </Button>
          ))}
        </div>
      </div>

      <Card>
        <CardHeader className="py-2">
          <CardTitle className="flex items-center gap-2">
            <span className="inline-block h-1.5 w-1.5 rounded-full bg-emerald-400 animate-pulse" />
            Live stream · {visibleEntries.length.toLocaleString()} entries
          </CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          <div
            ref={parentRef}
            onScroll={handleScroll}
            className="h-72 overflow-auto font-mono text-[11px] bg-slate-950 rounded-b-lg px-2"
          >
            <div
              style={{
                height: `${rowVirtualizer.getTotalSize()}px`,
                position: "relative",
              }}
            >
              {rowVirtualizer.getVirtualItems().map((vRow) => {
                const entry = visibleEntries[vRow.index];
                const hp = isHighPriority(entry.message);
                return (
                  <div
                    key={vRow.index}
                    style={{
                      position: "absolute",
                      top: 0,
                      left: 0,
                      width: "100%",
                      transform: `translateY(${vRow.start}px)`,
                      height: `${vRow.size}px`,
                    }}
                    className={`flex items-start gap-2 px-1 leading-5 ${
                      LEVEL_BG[entry.level as LogLevel]
                    } ${hp ? "border-l-2 border-yellow-500 pl-2" : ""}`}
                  >
                    <span className="shrink-0 text-slate-600 select-none">
                      {new Date(entry.ts).toLocaleTimeString("en", {
                        hour12: false,
                        hour: "2-digit",
                        minute: "2-digit",
                        second: "2-digit",
                      })}
                    </span>
                    <span
                      className={`shrink-0 w-12 font-semibold ${
                        LEVEL_COLORS[entry.level as LogLevel] ?? "text-slate-300"
                      }`}
                    >
                      {entry.level}
                    </span>
                    <span
                      className={`break-all ${
                        hp ? "text-yellow-300" : "text-slate-300"
                      }`}
                    >
                      {entry.message}
                    </span>
                  </div>
                );
              })}
            </div>
          </div>
        </CardContent>
      </Card>
    </section>
  );
});
