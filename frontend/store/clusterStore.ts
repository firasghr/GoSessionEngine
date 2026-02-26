import { create } from "zustand";

export interface NodeStatus {
  id: string;
  role: "master" | "worker";
  status: "online" | "offline" | "syncing";
  memory_mb: number;
  goroutines: number;
  grpc_status: "online" | "offline" | "syncing";
}

interface ClusterState {
  nodes: NodeStatus[];
  setNodes: (nodes: NodeStatus[]) => void;
  lastUpdated: number | null;
}

export const useClusterStore = create<ClusterState>((set) => ({
  nodes: [],
  lastUpdated: null,
  setNodes: (nodes) => set({ nodes, lastUpdated: Date.now() }),
}));
