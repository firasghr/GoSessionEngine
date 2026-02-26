"use client";

import * as React from "react";
import { cn } from "@/lib/utils";

export interface BadgeProps extends React.HTMLAttributes<HTMLSpanElement> {
  variant?: "default" | "success" | "warning" | "error" | "syncing";
}

const variantClasses: Record<NonNullable<BadgeProps["variant"]>, string> = {
  default: "bg-slate-600 text-slate-100",
  success: "bg-emerald-900/60 text-emerald-400 border border-emerald-700",
  warning: "bg-yellow-900/60 text-yellow-400 border border-yellow-700",
  error: "bg-red-900/60 text-red-400 border border-red-700",
  syncing: "bg-blue-900/60 text-blue-400 border border-blue-700",
};

export function Badge({ className, variant = "default", ...props }: BadgeProps) {
  return (
    <span
      className={cn(
        "inline-flex items-center rounded px-1.5 py-0.5 text-xs font-medium",
        variantClasses[variant],
        className
      )}
      {...props}
    />
  );
}
