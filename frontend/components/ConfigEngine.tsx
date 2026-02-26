"use client";

import React, { memo, useState, useCallback, useEffect } from "react";
import { useDropzone } from "react-dropzone";
import { Upload, Save, RotateCcw } from "lucide-react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { Slider } from "@/components/ui/slider";

const BACKEND = process.env.NEXT_PUBLIC_BACKEND_URL ?? "http://localhost:8080";

interface ConfigForm {
  target_url: string;
  number_of_sessions: number;
  max_retries: number;
}

const DEFAULT_FORM: ConfigForm = {
  target_url: "",
  number_of_sessions: 500,
  max_retries: 3,
};

export const ConfigEngine = memo(function ConfigEngine() {
  const [form, setForm] = useState<ConfigForm>(DEFAULT_FORM);
  const [status, setStatus] = useState<"idle" | "saving" | "saved" | "error">("idle");
  const [proxyStatus, setProxyStatus] = useState<"idle" | "uploading" | "done" | "error">("idle");
  const [proxyFileName, setProxyFileName] = useState<string>("");

  // Fetch current config on mount
  useEffect(() => {
    fetch(`${BACKEND}/api/config`)
      .then((r) => r.json())
      .then((data) => {
        setForm({
          target_url: data.target_url ?? "",
          number_of_sessions: data.number_of_sessions ?? 500,
          max_retries: data.max_retries ?? 3,
        });
      })
      .catch(() => {/* ignore */});
  }, []);

  const handleSave = async () => {
    setStatus("saving");
    try {
      const res = await fetch(`${BACKEND}/api/config`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          target_url: form.target_url,
          number_of_sessions: form.number_of_sessions,
          max_retries: form.max_retries,
        }),
      });
      setStatus(res.ok ? "saved" : "error");
      setTimeout(() => setStatus("idle"), 2_000);
    } catch {
      setStatus("error");
      setTimeout(() => setStatus("idle"), 2_000);
    }
  };

  const onDrop = useCallback(async (accepted: File[]) => {
    const file = accepted[0];
    if (!file) return;
    setProxyFileName(file.name);
    setProxyStatus("uploading");
    const fd = new FormData();
    fd.append("proxies", file);
    try {
      const res = await fetch(`${BACKEND}/api/proxy`, { method: "POST", body: fd });
      setProxyStatus(res.ok ? "done" : "error");
      setTimeout(() => setProxyStatus("idle"), 3_000);
    } catch {
      setProxyStatus("error");
      setTimeout(() => setProxyStatus("idle"), 3_000);
    }
  }, []);

  const { getRootProps, getInputProps, isDragActive } = useDropzone({
    onDrop,
    accept: { "text/plain": [".txt"] },
    maxFiles: 1,
  });

  return (
    <section className="space-y-4">
      <h2 className="text-sm font-semibold uppercase tracking-widest text-slate-400">
        Configuration Engine
      </h2>

      <Card>
        <CardHeader>
          <CardTitle>Engine Parameters</CardTitle>
        </CardHeader>
        <CardContent className="space-y-5">
          {/* Target URL */}
          <div className="space-y-1.5">
            <label className="text-xs text-slate-400">Target URL</label>
            <Input
              type="url"
              placeholder="https://target.example.com"
              value={form.target_url}
              onChange={(e) => setForm((f) => ({ ...f, target_url: e.target.value }))}
            />
          </div>

          {/* Concurrency slider */}
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <label className="text-xs text-slate-400">Concurrency Limit</label>
              <span className="text-xs font-mono text-cyan-400">{form.number_of_sessions}</span>
            </div>
            <Slider
              min={1}
              max={2000}
              step={1}
              value={[form.number_of_sessions]}
              onValueChange={([v]) => setForm((f) => ({ ...f, number_of_sessions: v }))}
            />
            <div className="flex justify-between text-[10px] text-slate-600">
              <span>1</span>
              <span>2000</span>
            </div>
          </div>

          {/* Max retries */}
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <label className="text-xs text-slate-400">Max Retries</label>
              <span className="text-xs font-mono text-cyan-400">{form.max_retries}</span>
            </div>
            <Slider
              min={0}
              max={10}
              step={1}
              value={[form.max_retries]}
              onValueChange={([v]) => setForm((f) => ({ ...f, max_retries: v }))}
            />
          </div>

          {/* Save / Reset */}
          <div className="flex gap-2">
            <Button onClick={handleSave} disabled={status === "saving"}>
              <Save size={12} className="mr-1" />
              {status === "saving" ? "Saving…" : status === "saved" ? "Saved ✓" : status === "error" ? "Error ✗" : "Apply"}
            </Button>
            <Button
              variant="outline"
              onClick={() => setForm(DEFAULT_FORM)}
            >
              <RotateCcw size={12} className="mr-1" />
              Reset
            </Button>
          </div>
        </CardContent>
      </Card>

      {/* Proxy upload */}
      <Card>
        <CardHeader>
          <CardTitle>Proxy List Upload</CardTitle>
        </CardHeader>
        <CardContent>
          <div
            {...getRootProps()}
            className={`flex flex-col items-center justify-center gap-2 rounded-lg border-2 border-dashed px-4 py-8 cursor-pointer transition-colors ${
              isDragActive
                ? "border-cyan-500 bg-cyan-900/20"
                : "border-slate-600 hover:border-slate-500 hover:bg-slate-700/30"
            }`}
          >
            <input {...getInputProps()} />
            <Upload size={24} className="text-slate-500" />
            <p className="text-xs text-slate-400 text-center">
              {isDragActive
                ? "Drop the .txt proxy file here"
                : "Drag & drop a proxy .txt file, or click to browse"}
            </p>
            {proxyFileName && (
              <p className="text-[10px] font-mono text-slate-500">{proxyFileName}</p>
            )}
            {proxyStatus === "uploading" && (
              <p className="text-xs text-blue-400">Uploading…</p>
            )}
            {proxyStatus === "done" && (
              <p className="text-xs text-emerald-400">Upload successful ✓</p>
            )}
            {proxyStatus === "error" && (
              <p className="text-xs text-red-400">Upload failed ✗</p>
            )}
          </div>
        </CardContent>
      </Card>
    </section>
  );
});
