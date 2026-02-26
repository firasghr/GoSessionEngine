import { create } from "zustand";

export type LogLevel = "INFO" | "DEBUG" | "ERROR";

export interface LogEntry {
  ts: number;
  level: LogLevel;
  message: string;
}

interface LogState {
  entries: LogEntry[];
  filters: Record<LogLevel, boolean>;
  addEntry: (e: LogEntry) => void;
  toggleFilter: (level: LogLevel) => void;
}

const MAX_ENTRIES = 5_000;

export const useLogStore = create<LogState>((set) => ({
  entries: [],
  filters: { INFO: true, DEBUG: true, ERROR: true },
  addEntry: (e) =>
    set((state) => ({
      entries:
        state.entries.length >= MAX_ENTRIES
          ? [...state.entries.slice(1), e]
          : [...state.entries, e],
    })),
  toggleFilter: (level) =>
    set((state) => ({
      filters: { ...state.filters, [level]: !state.filters[level] },
    })),
}));
