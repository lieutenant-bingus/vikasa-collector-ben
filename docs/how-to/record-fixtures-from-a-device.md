# Record fixtures from a device

**At the end** you will have a fixture file for your adapter's golden tests,
recorded from what a real device actually returned and carrying where it
came from — the bar
["No fixtures, no merge"](../reference/invariants.md#no-fixtures-no-merge)
(ADR 0008) holds an adapter PR to.

**You need** the device (or a simulator that faithfully reproduces one) on
a network you can reach, a `collector.yaml` with that device in it, and the
collector binary built from a tree that registers your adapter. These steps
are for SNMP, the one transport that ships today. The recorder records
any adapter that dials through `sdk/transport/snmp`; an adapter on another
transport has no recorder yet and follows the manual steps at the end.

## How the recorder works

`collector record-fixture` polls one configured device once, with the real
adapter, built through the same registry and connection block the collector
polls with. It records by wrapping the SNMP client that adapter dials
(`snmp.InterceptDial`), so the file holds exactly the GETs the adapter
makes — including a GET it derives from an earlier answer, such as
`ntcip-asc` reading as many detector channels as the controller says it
has. A list of OIDs typed into a separate tool would miss those.

The file is JSON (`sdk/transport/snmp/snmpfixture`):

- `provenance` — adapter, make, model, firmware, SNMP version, date, and the
  recorder's version. All required: a file without them fails to load.
- `ints` and `octets` — exactly what the device answered, keyed by numeric
  OID. Octet strings are hex, with the text alongside when it is printable;
  a file whose text and hex disagree was edited by hand and fails to load.
- `unanswered` — OIDs the adapter asked for that the device did not
  answer. Replay leaves them out, which is how a device's "no such object"
  replays.
- `redacted` — OIDs whose values were replaced because they identified the
  deployment. Their values are synthetic.

## Steps

1. **Build the collector** from a tree with your adapter registered, and
   copy it to wherever reaches the device (the cabinet already has one if
   it runs the collector).
2. **Record a healthy poll.** Name the device from your config, and say
   what you are recording from:

   ```bash
   collector record-fixture -config collector.yaml -device dms-1 \
     -make Ledstar -model CTL24 -firmware V6.9n \
     -note "healthy, message on face" \
     -out internal/vendors/ntcip/testdata/dms-healthy.json
   ```

   The community and address come from the config, so they never go on a
   command line and are never written to the file. The command prints what
   it wrote and what the adapter decoded from the poll: check the decode
   while you are still at the device. If the poll fails, nothing is
   written.
3. **Record the other states your tests need.** If you can put the device
   into the other states your adapter decodes (flash, a fault, a blank
   sign), record each to its own file, with `-note` saying which state it
   is. An OID the device does not answer is a result worth keeping, and
   `unanswered` keeps it.
4. **Scrub what identifies the deployment.** The recorder refuses to write
   a file in which any octet string contains the device's address, a
   non-default community, or the config's site, agency, agency unit,
   collector id or device id. The error names the OID. Re-run with
   `-redact <oid>` for each one; it is replaced by `REDACTED` and listed in
   `redacted`. A name the recorder cannot know — a road name in a sign
   message, a location in `sysLocation` — is yours to spot in the printed
   text and redact the same way. Values the adapter decodes stay as
   recorded.
5. **Replay it in the golden test.** `snmptest.LoadFixture` loads the file
   into the same `snmptest.Static` hand-written fixtures use:

   ```go
   a := NewDMS("dms-1", snmptest.LoadFixture(t, "testdata/dms-healthy.json"))
   ```

   Then write the golden and failure tests on top of it, as
   [`add-a-vendor-adapter.md`'s "Meet the test bar"](add-a-vendor-adapter.md#meet-the-test-bar)
   describes. A failure case derived from a recording (an OID removed, a
   value changed) is built in the test from the loaded maps, never by
   editing the file.
6. **Optionally, keep a live test.** `internal/vendors/ntcip/dms_live_test.go`
   reads a real sign only when `DMS_SNMP_ADDR` is set and skips otherwise,
   so CI never needs the device and the address never enters the repo.

## Regenerating a fixture

Run the same command against the same make, model and firmware, with the
same `-out`. The file is written with sorted keys and one value per line,
so the diff shows exactly which values the device now answers differently.
Firmware changed? Record to a new file and keep the old one: each firmware
an adapter claims to support is evidence of its own.

Never edit a recorded file by hand. A value the device cannot be made to
produce belongs in a test-built variant of the loaded fixture, or a
separate hand-written fixture marked synthetic with the reason, never
inside a file whose provenance says it was recorded.

## Without the recorder

An adapter on a transport other than SNMP, or a recording made before the
recorder existed, is transcribed by hand:

1. Capture exactly what your adapter's `Read` requests, with the protocol
   version it uses (for SNMP, `snmpget -v1 -c <community> -On <host>
   <oid> ...`; use GET by explicit OID if that is what the adapter does —
   some agents behave differently for walks).
2. Transcribe the values into the test's fake exactly as returned: do not
   round, rescale or tidy them.
3. Write the provenance comment directly above the fixture: what you
   captured, from which make, model and firmware (or which simulator), how,
   and the date. Mark any value you wrote by hand as synthetic, and say why.
   `healthyDMSInts` in `internal/vendors/ntcip/dms_test.go` is the worked
   example.
4. Scrub addresses, community strings, and site and agency names before you
   commit.

A hand transcription is checked only in review — see
[`testing-strategy.md`'s "Provenance is a review question, not a test"](../explanation/testing-strategy.md#provenance-is-a-review-question-not-a-test).
`ntcip-asc`'s fixtures were hand-typed and are not a model; re-recording
them against a real controller is a
[known gap](../README.md#the-code-does-not-meet-a-bar-the-docs-correctly-state).
