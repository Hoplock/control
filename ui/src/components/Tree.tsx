// TREE (DESIGN.md §7): the fleet graph's zones and proxies, a hop path. The
// tree pattern: one tab stop, the arrows move between visible items and open
// and close a branch (mirrored in a right-to-left page), Home and End go to
// the ends.

import { useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { useI18n } from "../i18n/I18n";
import styles from "./Tree.module.css";

export interface TreeNode {
  id: string;
  label: ReactNode;
  /** Shown after the label: a status, a count. */
  meta?: ReactNode;
  children?: readonly TreeNode[];
}

export function Tree({ label, nodes, onOpen }: { label: string; nodes: readonly TreeNode[]; onOpen?(id: string): void }) {
  const { dir } = useI18n();
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set(nodes.map((n) => n.id)));
  const [active, setActive] = useState<string | null>(nodes[0]?.id ?? null);
  const root = useRef<HTMLUListElement>(null);
  const visible: { node: TreeNode; parent: string | null }[] = [];
  const walk = (ns: readonly TreeNode[], parent: string | null) => {
    for (const n of ns) {
      visible.push({ node: n, parent });
      if (n.children?.length && expanded.has(n.id)) walk(n.children, n.id);
    }
  };
  walk(nodes, null);
  const focus = (id: string) => {
    setActive(id);
    root.current?.querySelector<HTMLElement>(`[data-node="${CSS.escape(id)}"]`)?.focus();
  };
  const toggle = (id: string, open: boolean) => {
    setExpanded((s) => {
      const next = new Set(s);
      if (open) next.add(id);
      else next.delete(id);
      return next;
    });
  };
  const keys = (e: KeyboardEvent) => {
    const at = visible.findIndex((v) => v.node.id === active);
    const cur = visible[at];
    if (!cur) return;
    const into = dir === "rtl" ? "ArrowLeft" : "ArrowRight";
    const out = dir === "rtl" ? "ArrowRight" : "ArrowLeft";
    const branch = Boolean(cur.node.children?.length);
    let handled = true;
    if (e.key === "ArrowDown" && at < visible.length - 1) focus((visible[at + 1] as (typeof visible)[number]).node.id);
    else if (e.key === "ArrowUp" && at > 0) focus((visible[at - 1] as (typeof visible)[number]).node.id);
    else if (e.key === "Home") focus((visible[0] as (typeof visible)[number]).node.id);
    else if (e.key === "End") focus((visible[visible.length - 1] as (typeof visible)[number]).node.id);
    else if (e.key === into && branch && !expanded.has(cur.node.id)) toggle(cur.node.id, true);
    else if (e.key === into && branch) focus((cur.node.children as TreeNode[])[0]?.id ?? cur.node.id);
    else if (e.key === out && branch && expanded.has(cur.node.id)) toggle(cur.node.id, false);
    else if (e.key === out && cur.parent) focus(cur.parent);
    else if (e.key === "Enter" && onOpen) onOpen(cur.node.id);
    else handled = false;
    if (handled) e.preventDefault();
  };
  const render = (ns: readonly TreeNode[], level: number): ReactNode =>
    ns.map((n) => {
      const branch = Boolean(n.children?.length);
      const open = expanded.has(n.id);
      return (
        <li
          key={n.id}
          role="treeitem"
          aria-level={level}
          aria-expanded={branch ? open : undefined}
          aria-selected={n.id === active}
          data-node={n.id}
          tabIndex={n.id === active ? 0 : -1}
          className={styles.item}
          onClick={(e) => {
            e.stopPropagation();
            setActive(n.id);
            if (branch) toggle(n.id, !open);
            else onOpen?.(n.id);
          }}
        >
          <span className={styles.line}>
            <span className={styles.twisty} aria-hidden="true" data-open={branch ? open : undefined}>
              {branch ? (
                <svg viewBox="0 0 10 10">
                  <path d="M3.5 2 6.5 5 3.5 8" />
                </svg>
              ) : null}
            </span>
            <span className={styles.label}>{n.label}</span>
            {n.meta ? <span className={styles.meta}>{n.meta}</span> : null}
          </span>
          {branch && open ? (
            <ul role="group" className={styles.group}>
              {render(n.children as TreeNode[], level + 1)}
            </ul>
          ) : null}
        </li>
      );
    });
  return (
    <ul ref={root} role="tree" aria-label={label} className={styles.tree} onKeyDown={keys}>
      {render(nodes, 1)}
    </ul>
  );
}
