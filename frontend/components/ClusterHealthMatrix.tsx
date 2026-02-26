"use client";

import React, { memo, useEffect, useCallback } from "react";
import { Cpu, Server, Wifi, WifiOff, RefreshCw } from "lucide-react";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { useClusterStore, NodeStatus } from "@/store/clusterStore";

const BACKEND = process.env.NEXT_PUBLIC_BACKEND_URL ?? "http://localhost:8080";

// ─── Node Card ───────────────────────────────────────────────────────────────

const NodeCard = memo(function NodeCard({ node }: { node: NodeStatus }) {
  const statusVariant =
    node.status === "online"
      ? "success"
      : node.status === "syncing"
      ? "syncing"
      : "error";

  const grpcIcon =
    node.grpc_status === "online" ? (
      <Wifi size={12} className="text-emerald-400" />
    ) : node.grpc_status === "syncing" ? (
      <RefreshCw size={12} className="text-blue-400 animate-spin" />
    ) : (
      <WifiOff size={12} className="text-red-400" />
    );

  return (
    <Card className="p-3 space-y-2">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-1.5">
          {node.role === "master" ? (
            <Server size={14} className="text-yellow-400" />
          ) : (
            <Cpu size={14} className="text-slate-400" />
          )}
          <span className="text-xs font-mono text-slate-200">{node.id}</span>
        </div>
        <Badge variant={statusVariant}>{node.status}</Badge>
      </div>

      <div className="grid grid-cols-3 gap-1 text-[10px] text-slate-400">
        <div className="flex flex-col">
          <span className="text-slate-500">RAM</span>
          <span className="font-mono text-slate-200">
            {node.memory_mb > 0 ? `${node.memory_mb} MB` : "—"}
          </span>
        </div>
        <div className="flex flex-col">
          <span className="text-slate-500">Goroutines</span>
          <span className="font-mono text-slate-200">
            {node.goroutines > 0 ? node.goroutines : "—"}
          </span>
        </div>
        <div className="flex flex-col">
          <span className="text-slate-500">gRPC</span>
          <span className="flex items-center gap-0.5">{grpcIcon}</span>
        </div>
      </div>
    </Card>
  );
});

// ─── Cluster Health Matrix ────────────────────────────────────────────────────

export const ClusterHealthMatrix = memo(function ClusterHealthMatrix() {
  const { nodes, setNodes, lastUpdated } = useClusterStore();

  const fetchNodes = useCallback(async () => {
    try {
      const res = await fetch(`${BACKEND}/api/nodes`);
      if (res.ok) {
        const data = await res.json();
        setNodes(data);
      }
    } catch {
      // backend unreachable – keep stale data
    }
  }, [setNodes]);

  useEffect(() => {
    fetchNodes();
    const id = setInterval(fetchNodes, 5_000);
    return () => clearInterval(id);
  }, [fetchNodes]);

  const master = nodes.find((n) => n.role === "master");
  const workers = nodes.filter((n) => n.role === "worker");
  const online = nodes.filter((n) => n.status === "online").length;

  return (
    <section className="space-y-4">
      <div className="flex items-center justify-between">
        <h2 className="text-sm font-semibold uppercase tracking-widest text-slate-400">
          Cluster Health Matrix
        </h2>
        <span className="text-xs text-slate-500">
          {online}/{nodes.length} online
          {lastUpdated ? ` · ${new Date(lastUpdated).toLocaleTimeString()}` : ""}
        </span>
      </div>

      <div className="space-y-3">
        {/* Master */}
        {master && (
          <div>
            <p className="text-[10px] uppercase tracking-widest text-yellow-600 mb-1">Master</p>
            <NodeCard node={master} />
          </div>
        )}

        {/* Workers */}
        {workers.length > 0 && (
          <div>
            <p className="text-[10px] uppercase tracking-widest text-slate-500 mb-1">Workers</p>
            <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
              {workers.map((n) => (
                <NodeCard key={n.id} node={n} />
              ))}
            </div>
          </div>
        )}

        {nodes.length === 0 && (
          <p className="text-xs text-slate-600 italic">Connecting to cluster…</p>
        )}
      </div>
    </section>
  );
});
