import { create } from "zustand";

export interface MetricsPoint {
  timestamp: number;
  total: number;
  success: number;
  failed: number;
  rps: number;
  sessions: number;
  cookieJarSize: number;
}

interface MetricsState {
  points: MetricsPoint[];
  latest: MetricsPoint | null;
  addPoint: (p: MetricsPoint) => void;
}

const MAX_POINTS = 120; // 12 seconds at 100 ms ticks

export const useMetricsStore = create<MetricsState>((set) => ({
  points: [],
  latest: null,
  addPoint: (p) =>
    set((state) => ({
      points:
        state.points.length >= MAX_POINTS
          ? [...state.points.slice(1), p]
          : [...state.points, p],
      latest: p,
    })),
}));
