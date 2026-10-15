# Changelog

## [0.1.0](https://github.com/lieutenant-bingus/vikasa-collector-ben/compare/v0.0.1...v0.1.0) (2026-10-15)


### ⚠ BREAKING CHANGES

* a -nats URL with user:pass@ or token@, or a plaintext non-loopback -nats, now fails boot. Use -nats-creds and tls://.
* a signal controller's reachability is published as openits.signal-control.comm-health-event.v1 instead of openits-collector.health.device-status-changed.v1, which now carries only device kinds without a catalog channel. Adapter panics no longer produce an unreachable transition.
* openits-collector.health.collector-started.v1 becomes openits-collector.instance.started.v1, on subject …<agency_unit>.instance.<site>.started instead of …<agency_unit>.health.collector.collector-started, with a new payload. sdk/model no longer exports CollectorStarted. Device-less subjects render the site, not "collector".
* **sdk:** sdk/adapter no longer exports EventReader, Commander, CapEvents or CapCommand, and sdk/model no longer exports Command or SetPlan. Adapters implement StateReader and set Caps: adapter.CapState.
* openits.perception.zone-interval-report.v1 no longer carries observed-count or occupancy-percent; consumers that read either must subscribe to openits.zone-occupancy.zone-occupancy-interval-report.v1. Every perception payload's object-class identityref changes module prefix.

### Features

* adopt openits-models v0.3.0 and map the zone-occupancy service ([ece05e6](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/ece05e66cea4d0a0657d798c80a657d12bb2bb1f))
* adopt openits-models v0.4.0 ([675d865](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/675d865c44ff81935dca1a77cfd0dabb070caded))
* broker credentials from a file, TLS off loopback, and a tested least-privilege grant ([22004e2](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/22004e2d4b7558d0784fa5c73be7e70d57398ae5))
* **cli:** configurable logging, a testable run(), and a logger injected into app ([c52cb8f](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/c52cb8f9a4b0129e6813c00418ccadcfe95e8754))
* **cli:** configurable logging, a testable run(), and a logger injected into app ([0ae3b23](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/0ae3b23ac5e3f3f7dcc5e0f4d6ac25fc227748a3))
* CloudEvents binary mode, ULID ce-id, and the profile source URN ([a724d76](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/a724d76032663c8e0b464138d343a2c44242a358))
* collector self-health heartbeat on the openits-collector.instance root (ADR 0019) ([d75b5d8](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/d75b5d88f1d6443353af2a8de38977c716071517))
* **dev:** add `make dev`, and route contributors to the bar they are judged against ([7b4a89e](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/7b4a89e90cbd9fab4de9e7bec1f75ad864fb691c))
* device reachability on the catalog's comm-health-event; adapter faults are not device faults (ADR 0019) ([c840d56](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/c840d561666dd4efbf5642054029987dbf01b5d7))
* **dms:** carry the face message and environment cluster, and map the v0.4.0 DMS event surface ([a0dac37](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/a0dac37eb83dca40e714648b6c398751cb941a69))
* **durable:** previous synth state survives restarts in JetStream KV (ADR 0017) ([b533c3c](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/b533c3cebdd264fbfefe2f2047cd2e9108be7195))
* **durable:** previous synth state survives restarts in JetStream KV (ADR 0017) ([2b6a8fa](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/2b6a8fab134581748b346c08148527dacf825981))
* **logship:** publish the collector's own warn+ logs as instance.log events (ADR 0019 clause 10) ([23911c2](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/23911c2fbb7a29b5332622e0ad01cf4b40a769e2))
* **logship:** publish the collector's own warn+ logs as instance.log events (ADR 0019 clause 10) ([a7be1e5](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/a7be1e547ec5e72fcc703b45776222bc65caba2c))
* **manage:** loopback management surface — /livez, latching /readyz, /status ([b13ac6c](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/b13ac6cd687b83e941c512afe0a8d830bf1efeb9))
* **manage:** loopback management surface — /livez, latching /readyz, /status ([4ba1864](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/4ba186473c234cd66be807b806d6a80b52feb5d5))
* model cctv control mode and tour state ([7fc5ace](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/7fc5ace569fe48a69987e15d0ab1892b7f38ec98))
* model perception zone interval reports ([248917f](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/248917f45d869efc9e65434192b902fb322a5acd))
* model perception zone-incident lifecycle end to end ([fdec971](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/fdec971920614dda3bed973e1ddf2e29cc0f5445))
* model traffic-sensor interval reports end to end ([63d1dbf](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/63d1dbfa55312d0c484139933956080480fc05b2))
* **ntcip:** add ntcip-dms adapter for NTCIP 1203 signs ([391ab64](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/391ab64e38b12f4a99ae9f5832d4e819cce4a342))
* **ntcip:** adopt openits-models v0.4.0 for DMS wire surface ([b60b4b3](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/b60b4b3ad6d2293e496aaf509adb8b334c2989d9))
* pin openits-models on release tags, enforced by lint Rule D ([b6241f8](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/b6241f84c51813cf80e7c7802363d2b3a3afb857))
* provision a stream per namespace and keep health off the catalog space ([ea56655](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/ea566553d3bc27a5a34a8111a385d84b97e4db74))
* **publish:** background broker connect and a bounded publish queue (ADR 0020) ([24b63f9](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/24b63f9371e966032f0b10a945ce2789b9783ac0))
* **publish:** background broker connect and a bounded publish queue (ADR 0020) ([97bb89c](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/97bb89ce1cc5d370d35bfc8eaf325c2c97e89948))
* **release:** linux amd64/arm64 release artifacts, a hardened systemd unit, and a real deploy guide ([8606d2e](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/8606d2e48fb3832f413a9215f107f87bed3b6ce4))
* **release:** linux amd64/arm64 release artifacts, a hardened systemd unit, and a real deploy guide ([1742993](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/1742993983dbcb608a0cfbb9b42c50bf97c615d0))
* **sdk:** usable zero-value Registry and an ErrNotRegistered sentinel ([c290912](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/c290912934eff2715a1493fd2fa489b3645abce3))
* **stats:** one shared Counters type for the collector's own metrics ([a573a66](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/a573a66fa6f52f048450e46e9375590c864c4c9e))
* **stats:** one shared Counters type for the collector's own metrics ([276a2c9](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/276a2c9d1f74146555ba095e4d537a457dfad2cb))
* **subject:** adopt the profile's seven-token grammar as the default ([e9f3640](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/e9f36406160a2792c6f014cfdfdf4c1e82d2b87f))
* **subject:** root subjects on the ce-type namespace ([f0ff037](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/f0ff037bfc791d6b4a89c502b5dc564c208d51c8))
* **synth:** hold, seed and change-hook support for durable engine state ([6d98311](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/6d98311ec5bd358d4219f04ba7e4bc3e91ee25a9))
* wire the openits emitter into the chain behind a required collector_id ([1e4f144](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/1e4f14497f5aaccb45362741ec48dec28520cf86))
* **wire:** carry ce-dataschema pinned to the defining events module ([bd97d31](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/bd97d317f48d11d4c8291ef0b4b59ffe34e85ef8))
* **wire:** complete the domain-to-ce-type mapping table ([3992d7e](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/3992d7ea5cc82b86a5a864de0e6c6d4f4fdc6563))
* **wire:** extend the shared fault family to cctv, traffic-sensor and perception ([f358b03](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/f358b0317d00afffad1478d98748dc804c423bad))
* **wire:** map both DMS mode axes onto the shared ce-type ([16cec8f](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/16cec8f6aa45309de56c23a3da354a17b320c19a))
* **wire:** map controller modes to upstream identities ([034f56b](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/034f56bc59648049aec316def364a798fbdcf7f3))
* **wire:** map the shared fault events across both services ([bca986c](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/bca986c90219e4a84e680c32c98d0d5840235184))
* **wire:** openits-models emitter skeleton with tuple dispatch ([780d4e2](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/780d4e2f6727c353cd43a63068376f88db8003b9))
* **wire:** populate the mandatory event-header leaves ([e50e629](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/e50e629aaccedc12ca16f3cd5b30cae31d56dd93))


### Bug Fixes

* **app:** build every adapter before announcing or starting anything ([1cb1230](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/1cb123006f2ff7b2f79381f7f50a34f642f877ff))
* bound shutdown — hung reads, SNMP context, adapter Close, second signal ([0695c88](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/0695c8825ea099161abf8db7517d4d8bf2df2be4))
* bound shutdown — hung reads, SNMP context, adapter Close, second signal ([d56199b](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/d56199b603938d70c77b3f34a78582e7ffdf4aba))
* **build:** build with go1.26.9; go1.26.0 left 28 reachable stdlib vulnerabilities ([35079fa](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/35079fa4334ad23f3808813cee5a95b642ee8574))
* **ci:** enforce ADR 0010's never-replace clause ([5a25d71](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/5a25d713ed0b794a5782241c392f7d8984579999))
* **ci:** lint relative links in .claude/skills, and fix the template's depth ([0495e8d](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/0495e8d95f82de3481b53fe89b3be5d70024720d))
* **ci:** make three guards check the thing they claim ([10eb5e5](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/10eb5e51058b8d1ab27f155af635bf33bffea231))
* **config:** fail boot on an unsupported model_version, and select by it ([1d9e50f](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/1d9e50f91c47c699b6d0efa9f105228de75d7625))
* **config:** fail boot on an unsupported model_version, and select by it ([0d08732](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/0d0873234b41f85cc7637d2c8fa7b3b4be5dbad2))
* **config:** fail boot on unrecognized keys, as ADR 0014 promises ([de0b5c5](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/de0b5c53ca83de4383d291ebd1c948b3c6ded613))
* **deps:** align go.mod with go.sum on gosnmp v1.44.0 ([3e76e52](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/3e76e52f4e39350ffbddc9c9ebeebe9774918d40))
* **docs:** asyncapi addresses match the ADR 0011 grammar ([2da51f5](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/2da51f5f016d189cc22489ffe066dfb6830da35f))
* **docs:** correct ce-source, ce-id and ce-type envelope descriptions ([09351ed](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/09351edf6541801778faa2a17bd1b5ee4dba645b))
* **docs:** correct four false claims and document the command line ([c9a5588](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/c9a558891d81fce1f08efb2a59ad6d1beec8d81f))
* **docs:** correct the ce-type count, guard inventory and tier list ([04ab5f0](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/04ab5f049ec8c8d19a1a93f844fbf4c089f4f414))
* **docs:** correct the paths, citations and status lines review found ([7c70b9f](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/7c70b9f99f1c6770003a98807af86454e9c863ef))
* enforce the boot trust boundary (strict config, clean boot failure) ([dc2b2ce](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/dc2b2ce3d314e370663c96a23b27d3fde2abdf0e))
* **manage:** refuse non-loopback Host headers (DNS rebinding) ([c946258](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/c9462588db8ae8cbbab41031902a769572375232))
* **ntcip-dms:** harden partial reads and record environment fixture ([b9611b6](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/b9611b6bf92207c6a6d87696d5631ed48c8a693b))
* **ntcip-dms:** keep env facet when humidity table is empty ([a49c759](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/a49c759d4f7d8a84faca01f488fb66e556c07b23))
* **ntcip-dms:** reject malformed message identity ([fac5433](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/fac5433ff93bec38e5ca19cddf14d6102c020adc))
* **packaging:** the systemd unit could not read its own config ([65ffb19](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/65ffb1972d7ba06e10d866e66d65eda7dd75b752))
* **publish:** never log or return credentials embedded in the NATS URL ([ad5f20b](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/ad5f20b5e41fd708db4cd6fd1d44249341114c58))
* **publish:** scrub credentials by literal replacement, not by pattern ([5009e10](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/5009e10fc130bf9b503ea791b0563e51ec5d9bf1))
* **publish:** scrub credentials from errors, not just from the URL we print ([572c299](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/572c2999cb940684925ec770900a58b8a301f14b))
* **synth:** reject wrong-typed facets and honor Snapshot.Errors in the engine ([86d4ca5](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/86d4ca5132087eb1af4fa5cb5320ef67ee4e27a4))
* **synth:** reject wrong-typed facets and honor Snapshot.Errors in the engine ([8021d0d](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/8021d0dce0269b95a58afa3e11401e630b4cd450))


### Code Refactoring

* **sdk:** remove the unimplemented EventReader and Commander surface ([dc376b6](https://github.com/lieutenant-bingus/vikasa-collector-ben/commit/dc376b6f9832c425647ba06cbccb167d912650c9))
