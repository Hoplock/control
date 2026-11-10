// The inventory's rules (0054), held without a browser: how a form's label
// set and lists are read, how a grant's state reads, which field a refusal is
// about, and that every role the server defines has a description here.

import { describe, expect, it } from "vitest";
import { STATUS } from "../src/components/Status";
import en from "../src/locales/en.json";
import {
  bindingKey,
  grantStatus,
  holderPath,
  labelFilters,
  labelsText,
  parseLabels,
  parseList,
  problemKey,
  PROBLEMS,
  refusedField,
  roleKey,
  ROLES,
} from "../src/screens/inventoryModel";
import table from "./north-routes.json";

describe("a label set in a form", () => {
  it("is written one key=value per line, keys in order, and read back", () => {
    const text = labelsText({ kind: "db", env: "prod" });
    expect(text).toBe("env=prod\nkind=db");
    expect(parseLabels(text)).toEqual({ env: "prod", kind: "db" });
  });
  it("reads commas as well as lines, trims, and skips blanks", () => {
    expect(parseLabels(" env = prod , \n\n kind=db ")).toEqual({ env: "prod", kind: "db" });
    expect(parseLabels("")).toEqual({});
    expect(parseLabels("empty=")).toEqual({ empty: "" });
  });
  it("refuses an entry with no key rather than guessing one", () => {
    expect(parseLabels("env")).toBeNull();
    expect(parseLabels("=prod")).toBeNull();
  });
});

describe("a list in a form", () => {
  it("is one per line or comma, trimmed, with blanks and repeats dropped", () => {
    expect(parseList("ada\n bob ,ada,,\n")).toEqual(["ada", "bob"]);
  });
});

describe("the target list's label filter", () => {
  it("is the API's own grammar, repeatable, and a malformed one is not sent", () => {
    expect(labelFilters(new URLSearchParams("label=env=prod&label=kind=db&label=bare"))).toEqual(["env=prod", "kind=db"]);
  });
});

describe("a grant's state", () => {
  it("reads in the status vocabulary: live allows, a window not yet open is information, an ended one is deny-class", () => {
    expect(STATUS[grantStatus("active")].tone).toBe("allow");
    expect(STATUS[grantStatus("scheduled")].tone).toBe("info");
    expect(STATUS[grantStatus("expired")].tone).toBe("deny");
    expect(STATUS[grantStatus("revoked")].tone).toBe("deny");
  });
  it("is never read as fine when the server names a state this console does not know", () => {
    expect(grantStatus("suspended")).toBe("notReported");
  });
});

describe("a refused write", () => {
  it("names its field only for invalid_request with a field", () => {
    expect(refusedField({ code: "invalid_request", parameters: { field: "scope.name", problem: "required" } })).toBe("scope.name");
    expect(refusedField({ code: "invalid_request" })).toBeNull();
    expect(refusedField({ code: "forbidden", parameters: { field: "x" } })).toBeNull();
  });
  it("has a sentence for every problem the grant service names, and a fallback for any other", () => {
    for (const p of ["required", "invalid", "too_long", "too_many", "ambiguous", "not_after_start", "exceeds_maximum"]) {
      expect(PROBLEMS[p], p).toBeDefined();
    }
    expect(problemKey({ code: "invalid_request", parameters: { problem: "brand_new" } })).toBe("inventory.problem.other");
  });
});

describe("the fixed roles", () => {
  it("each role the server defines has a description here, and nothing more", () => {
    const server = Object.keys(table.roles).sort();
    expect(Object.keys(ROLES).sort()).toEqual(server);
    for (const r of server) expect(en[roleKey(r) as keyof typeof en], r).toBeTruthy();
  });
  it("a role this console does not know is shown by its code with the fallback", () => {
    expect(roleKey("auditor-plus")).toBe("inventory.role.unknown");
  });
});

describe("a role binding", () => {
  it("is keyed and linked by its one holder", () => {
    expect(bindingKey({ role: "admin", group_id: "sre" })).toBe("admin/group/sre");
    expect(bindingKey({ role: "admin", subject_id: "ada" })).toBe("admin/subject/ada");
    expect(holderPath({ role: "admin", group_id: "a b" })).toBe("inventory/groups/a%20b");
    expect(holderPath({ role: "admin", subject_id: "ada@example.com" })).toBe("inventory/subjects/ada%40example.com");
  });
});
