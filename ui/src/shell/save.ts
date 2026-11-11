// Saving a file the API produced (0056): the browser's own download, from
// memory, under the name the server gave it. Nothing leaves the origin: the
// bytes are already here, and an object URL is local to this page.

/** Saves bytes as a file the browser downloads. */
export function saveFile(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  a.rel = "noopener";
  document.body.append(a);
  a.click();
  a.remove();
  // The download has its own reference; this page's can go once it started.
  window.setTimeout(() => URL.revokeObjectURL(url), 0);
}
