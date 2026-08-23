---
name: add-transport
description: Build a new sdk/transport/<name> package — reusable dial/read plumbing for a protocol no adapter currently speaks — and its replay fake. Use this whenever a device does not speak SNMP and the request is "add an HTTP transport", "add a serial/Modbus transport", "my device isn't SNMP, what do I do", "write a transport for this protocol", or "add a new sdk/transport package". Does not cover writing the adapter itself that consumes the transport.
contract: v1
---

# Adding a transport

Exactly one transport exists in this repo today, `sdk/transport/snmp`. This
skill generalises from it honestly: where SNMP's shape is a genuine
protocol constraint (a PDU size cap, a connection that cannot be shared
across goroutines), that constraint is named as SNMP's, not smuggled in as
a rule every transport must follow. Where SNMP's shape is just SNMP's
shape, this skill says so and tells you to find your own protocol's
equivalent instead of copying a number.

## When this applies

Building a new package under `sdk/transport/<name>/` because a device
speaks a protocol nothing under `sdk/transport/` already covers — HTTP,
serial, Modbus, or anything else. `ls sdk/transport/` to confirm the
protocol you need genuinely isn't there yet.

It does not apply to:

- Writing the adapter that calls into a transport — connection-block
  parsing, the `Factory`, and the `Read` method's facet-by-facet logic are
  the `add-vendor-adapter` skill's job, not this one's. That skill's
  ["Pick the transport, and parse the connection
  block"](../../../docs/how-to/add-a-vendor-adapter.md#pick-the-transport-and-parse-the-connection-block)
  section is where the two skills meet: it says an adapter parses its own
  connection block and calls a transport's `Dial`; this skill is about
  what that `Dial` and the `Client` it returns look like.
- A device that already speaks SNMP — use `sdk/transport/snmp` as-is,
  don't add a second SNMP-shaped package.

## Invariants

- [Adapters and `sdk/` never import openits-models](../../../docs/reference/invariants.md#adapters-and-sdk-never-import-openits-models) — `sdk/transport/<name>` is part of `sdk/`; reaching for an openits-models type, even transitively through a helper package, fails `scripts/lint-boundary.sh` exactly as it would for an adapter.
- [No fixtures, no merge](../../../docs/reference/invariants.md#no-fixtures-no-merge) — ADR 0008 assigns the replay fake to the transport package itself, not to the first adapter that uses it; a transport contributed without one leaves every adapter built on it unable to meet the fixture bar.

## Procedure

1. **Shape the package like `snmp` + `snmptest`.** `sdk/transport/<name>/`
   holds the client; `sdk/transport/<name>/<name>test/` holds its replay
   fake, as a separate package the way `snmptest` is separate from `snmp`.
   Open both `sdk/transport/snmp/client.go` and
   `sdk/transport/snmp/snmptest/static.go` before writing either half —
   the package doc comment on each states what it is in one line and is
   worth copying the *pattern* of, not the words.

2. **Draw the boundary: protocol plumbing here, device meaning in the
   adapter.** The transport knows the protocol's own primitives — address,
   credentials, timeout, retries, request/response shape — and nothing
   about what a given address or field *means* on the wire. `snmp.Client`
   is the model:

   ```go
   type Client interface {
       Get(ctx context.Context, oids []string) (map[string]int64, error)
       Close() error
   }
   ```

   `Get` takes OID strings and returns raw integers; it has no idea an OID
   means "operation status" or that a bit in the result is a conflict-flash
   flag. `internal/vendors/ntcip/asc.go`'s `readSignalStatus` and
   `readFaultSet` are where those raw integers become `model.SignalStatus`
   and `model.FaultSet` — on the adapter's side of the interface, never the
   transport's. If you find yourself importing `sdk/model` into your
   transport package, the boundary has moved to the wrong side.

3. **An entry missing from a response is absence, not zero.** `snmp.Client.Get`
   omits any OID the agent didn't answer from its result map rather than
   filling in `0` — the doc comment on `Client` says so explicitly, and
   `toInt64`'s `NoSuchObject`/`NoSuchInstance` branch is where that
   omission happens. Whatever shape your protocol's non-answer takes (a 404,
   a timeout on one field, an empty response body), preserve it as
   *absence* through your `Client`, not as a zero value — the adapter's
   facet code is what turns that absence into a `model.FacetError`
   (`add-vendor-adapter`'s job), and it can only do that if the transport
   didn't already erase the distinction.

4. **The adapter owns its transport instance entirely.** A `Client` is a
   small interface, not a concrete struct, precisely so an adapter's own
   struct can hold one as a field and a test can substitute the replay fake
   for it — `internal/vendors/ntcip/asc.go`'s `asc` struct holds a
   `client snmp.Client` field, and `NewASC(deviceID string, client
   snmp.Client)` takes the interface, injectable by both the real `Dial`
   and `snmptest.Static`. Shape your `Dial(cfg DialConfig) (Client, error)`
   the same way: a constructor that returns the interface, not the
   concrete type.

5. **Ship the replay fake in the same commit, not later.** Model it on
   `snmptest.Static`:
   - A struct implementing your `Client` interface, with a compile-time
     assertion (`var _ snmp.Client = (*Static)(nil)` is the pattern) so the
     fake can't silently drift out of sync with the real interface.
   - Replays a fixed set of recorded values; anything not in that set is
     omitted from the result, mirroring a live device's non-answer (see
     step 3).
   - If your adapter will issue more than one call per `Read` — `Static`'s
     `FailCall map[int]error` exists because `ntcip-asc` issues a scalar
     `Get` and then a separate detector-table `Get` — give your fake a way
     to fail one specific call by number while the rest succeed, so a test
     can exercise "this facet's read failed, that one didn't" without a
     second fake type.
   - This fake is what a fixture *replays into* — it is not a recorder.
     Nothing in this repo captures a live session into fixture data yet;
     [`record-fixtures-from-a-device.md`](../../../docs/how-to/record-fixtures-from-a-device.md)
     is the honest account of that gap and what to do meanwhile
     (hand-capture, with a provenance comment). Your fake existing is what
     makes that hand-capture usable at all — without it there is nothing
     for a hand-typed or hand-captured fixture to replay into, and no
     adapter built on your transport can reach the bar in
     [`test-requirements.md`'s "A new adapter" section](../../../docs/reference/test-requirements.md#a-new-adapter).

6. **Chunk to your own protocol's limit, not SNMP's.** `snmp.Client.Get`
   splits its OID list into batches of 16 before each round trip, with the
   comment `// gosnmp caps PDUs per request; chunk to stay portable across
   agents.` right above the constant — that number is what `gosnmp`'s PDU
   handling requires, not a general batching rule. If your protocol has an
   equivalent hard ceiling (an HTTP API's page size or URL-length limit, a
   Modbus register-count-per-request cap, a serial frame size), chunk to
   *that* number, transparently to the caller, and say in a comment which
   limit forced it — the way `client.go` does. If your protocol has no such
   ceiling, don't invent one.

7. **Serialize I/O yourself if the session can't take concurrent use.**
   `snmp.client` wraps every `Get`/`Close` in a mutex because "one gosnmp
   connection must never see concurrent use" (the comment on `Dial`) — a
   fact about that specific library, not about transports in general. If
   your underlying connection has the same constraint, your `Client`
   implementation is where that gets enforced; nothing upstream serializes
   it for you.

8. **Nothing outside the adapter changes.** A transport package is never
   registered with `adapter.Registry` on its own — only the adapter that
   calls it is. If adding your transport touches anything under
   `sdk/adapter/`, `internal/config/`, or `cmd/collector/main.go`, that's a
   sign the work has drifted into adapter or core territory this skill
   doesn't cover.

## Verify

```bash
make check
go test ./... -race
gofmt -l .
```

Expected: `make check` and `go test ./... -race` both pass; `gofmt -l .`
prints nothing.

```bash
go test ./sdk/transport/... -race -v
```

Expected: your new package and its `<name>test` fake both pass, including
the fake's compile-time `Client` assertion and any per-call failure
injection test you wrote for it.

## Canonical doc

[`docs/explanation/pluggability.md`](../../../docs/explanation/pluggability.md) — the full narrative on the adapter contract, the opaque `connection` block, and why transport never crosses into the core.
