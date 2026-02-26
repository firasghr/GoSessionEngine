"use client";

import React, { memo } from "react";
import {
  LineChart,
  Line,
  XAxis,
  YAxis,
  Tooltip,
  ResponsiveContainer,
  Legend,
} from "recharts";
import { Activity, CheckCircle, XCircle, Zap, Cookie, Database } from "lucide-react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { useMetricsStore } from "@/store/metricsStore";
import { useSSE } from "@/hooks/useSSE";

const BACKEND = process.env.NEXT_PUBLIC_BACKEND_URL ?? "http://localhost:8080";

// ─── KPI Card ────────────────────────────────────────────────────────────────

interface KPICardProps {
  title: string;
  value: string | number;
  sub?: string;
  icon: React.ReactNode;
  accentClass?: string;
}

const KPICard = memo(function KPICard({
  title,
  value,
  sub,
  icon,
  accentClass = "text-cyan-400",
}: KPICardProps) {
  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between pb-1">
        <CardTitle className="text-xs text-slate-400">{title}</CardTitle>
        <span className={accentClass}>{icon}</span>
      </CardHeader>
      <CardContent>
        <div className={`text-2xl font-bold font-mono ${accentClass}`}>{value}</div>
        {sub && <p className="text-xs text-slate-500 mt-0.5">{sub}</p>}
      </CardContent>
    </Card>
  );
});

// ─── Telemetry Dashboard ─────────────────────────────────────────────────────

export const TelemetryDashboard = memo(function TelemetryDashboard() {
  const addPoint = useMetricsStore((s) => s.addPoint);
  const points = useMetricsStore((s) => s.points);
  const latest = useMetricsStore((s) => s.latest);

  useSSE(`${BACKEND}/api/metrics/stream`, (raw) => {
    try {
      const p = JSON.parse(raw);
      addPoint({
        timestamp: p.timestamp,
        total: p.total,
        success: p.success,
        failed: p.failed,
        rps: Math.round(p.rps * 10) / 10,
        sessions: p.sessions,
        cookieJarSize: p.cookie_jar_size,
      });
    } catch {
      // ignore malformed frame
    }
  });

  const chartData = points.map((p) => ({
    t: new Date(p.timestamp).toLocaleTimeString("en", { hour12: false }),
    RPS: p.rps,
    Success: p.success,
    Failed: p.failed,
  }));

  return (
    <section className="space-y-4">
      <h2 className="text-sm font-semibold uppercase tracking-widest text-slate-400">
        Global Telemetry
      </h2>

      {/* KPI row */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
        <KPICard
          title="Active Sessions"
          value={latest?.sessions ?? "—"}
          icon={<Activity size={14} />}
          accentClass="text-cyan-400"
        />
        <KPICard
          title="RPS"
          value={latest?.rps?.toFixed(1) ?? "—"}
          sub="requests / sec"
          icon={<Zap size={14} />}
          accentClass="text-yellow-400"
        />
        <KPICard
          title="Total Requests"
          value={latest?.total?.toLocaleString() ?? "—"}
          icon={<Database size={14} />}
          accentClass="text-slate-300"
        />
        <KPICard
          title="Success"
          value={latest?.success?.toLocaleString() ?? "—"}
          icon={<CheckCircle size={14} />}
          accentClass="text-emerald-400"
        />
        <KPICard
          title="Failed"
          value={latest?.failed?.toLocaleString() ?? "—"}
          icon={<XCircle size={14} />}
          accentClass="text-red-400"
        />
        <KPICard
          title="Cookie Jar"
          value={latest?.cookieJarSize ?? "—"}
          sub="global entries"
          icon={<Cookie size={14} />}
          accentClass="text-purple-400"
        />
      </div>

      {/* Live chart */}
      <Card>
        <CardHeader>
          <CardTitle>Live RPS &amp; Success / Fail</CardTitle>
        </CardHeader>
        <CardContent className="h-48">
          <ResponsiveContainer width="100%" height="100%">
            <LineChart data={chartData} margin={{ top: 4, right: 8, left: -20, bottom: 0 }}>
              <XAxis
                dataKey="t"
                tick={{ fill: "#94a3b8", fontSize: 9 }}
                interval="preserveStartEnd"
              />
              <YAxis tick={{ fill: "#94a3b8", fontSize: 9 }} />
              <Tooltip
                contentStyle={{ background: "#1e293b", border: "1px solid #334155", fontSize: 11 }}
                labelStyle={{ color: "#94a3b8" }}
              />
              <Legend wrapperStyle={{ fontSize: 10, paddingTop: 4 }} />
              <Line
                type="monotone"
                dataKey="RPS"
                stroke="#22d3ee"
                dot={false}
                strokeWidth={1.5}
                isAnimationActive={false}
              />
              <Line
                type="monotone"
                dataKey="Success"
                stroke="#34d399"
                dot={false}
                strokeWidth={1.5}
                isAnimationActive={false}
              />
              <Line
                type="monotone"
                dataKey="Failed"
                stroke="#f87171"
                dot={false}
                strokeWidth={1.5}
                isAnimationActive={false}
              />
            </LineChart>
          </ResponsiveContainer>
        </CardContent>
      </Card>
    </section>
  );
});
