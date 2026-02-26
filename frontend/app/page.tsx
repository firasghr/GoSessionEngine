import { TelemetryDashboard } from "@/components/TelemetryDashboard";
import { ClusterHealthMatrix } from "@/components/ClusterHealthMatrix";
import { ConfigEngine } from "@/components/ConfigEngine";
import { LogTerminal } from "@/components/LogTerminal";
import { Terminal } from "lucide-react";

export default function CommandCenter() {
  return (
    <div className="min-h-screen bg-slate-950">
      {/* Top nav bar */}
      <header className="sticky top-0 z-50 border-b border-slate-800 bg-slate-950/90 backdrop-blur">
        <div className="mx-auto flex max-w-screen-2xl items-center gap-3 px-4 py-2.5">
          <Terminal size={18} className="text-cyan-400" />
          <span className="font-mono text-sm font-semibold text-slate-100">
            GoSessionEngine — Command Center
          </span>
          <span className="ml-auto font-mono text-[10px] text-slate-500">
            NOC v1.0
          </span>
        </div>
      </header>

      {/* Main grid */}
      <main className="mx-auto max-w-screen-2xl px-4 py-6 space-y-8">
        {/* Row 1: Telemetry (full width) */}
        <TelemetryDashboard />

        {/* Row 2: Cluster + Config side by side */}
        <div className="grid grid-cols-1 gap-8 lg:grid-cols-2">
          <ClusterHealthMatrix />
          <ConfigEngine />
        </div>

        {/* Row 3: Log terminal (full width) */}
        <LogTerminal />
      </main>
    </div>
  );
}
