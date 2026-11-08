# Traceability — CP3 (brought forward), 2026-11-09

Audit: spec-auditor (read-only, transcript `ai-sessions/subagents/07-review-spec-auditor-cp3.md`) at `d5636f2`,
then the orchestrator closed the P0/P1 findings; this file is the audit's matrix updated with that evidence.
Status: **green** = implemented + a check test that can fail, seen passing · **owner** = needs the owner.
Totals: 28 green, 1 owner (R-DEL-1). P2 gaps are listed in README → "Not done / next steps".

## Commands and results

| Command | Result |
|---|---|
| `make test` (containers, no internet) | 17 Go packages `ok`, Vitest `113 passed`; inside a clean clone: 97 s |
| `make fresh-clone-check` | clean clone, no `.env`: healthy in 13 s, 5000 users, mock LLM · restart: 5000 (idempotent) · `down -v && up`: 5000 · key-only `.env` (dummy key): anthropic mode, healthy · `fresh-clone-check: OK` |
| `make e2e` | `1 passed` (official brief → audience → streamed content → invalid edit blocked → saved edit → approve → dashboard + throughput chart → finished → reload) |
| `make stress` | REPORT.md: S1, S2 (25 000 users, 49 463 messages), S7 PASS · REPORT-api.md: S3, S5, S5b, S6, S8 PASS |
| `go test ./internal/segment -fuzz FuzzParseValidateCompile -fuzztime 2m` | 5.9 M executions, `PASS` |
| `make eval PROVIDER=claudecode` | 16/16 segments valid first try, 14/14 exact audiences, 16/16 content valid |
| `make sanitize-check` | gitleaks history + session exports clean, every export listed, no personal terms |
| `make lint` | golangci-lint `0 issues`, vue-tsc clean |

## Requirements

| ID | Status | Check tests (examples; all pass) | Stress / adversarial evidence |
|---|---|---|---|
| R-FLOW-1 | green | TestMockDemo_OfficialBrief_RFLOW1, TestCount_MatchesDirectSQL_RFLOW1, e2e | S5 / S5b (100k users, p95 40 ms); claudecode eval 14/14 exact audiences |
| R-FLOW-2 | green | **TestGenerateStream_MetaDeltasResult_RFLOW2**, **TestGenerateStream_ClientDisconnectCancelsUpstream_RFLOW2** (HTTP handler), TestStream_SplitMultibyteRune_RFLOW2, Vitest sse/partialJson, e2e | disconnect cancels the upstream call at service and HTTP level |
| R-FLOW-3 | green | TestApprove_Gating_RFLOW3, TestApprove_ConcurrentOneWins_RFLOW3, approveGate.test.ts, e2e | S3: 100 racers → `202:1 409:99`, replay byte-identical; 10 campaigns at once all succeed |
| R-FLOW-4 | green | TestDispatcher_EndToEnd_RFLOW4, e2e | S1; S2 at 25 000 users with a worker SIGKILLed: DB sent = provider accepted |
| R-FLOW-5 | green | TestEvents_SnapshotThenDone_RFLOW5, TestHub_SharedPollAndDone_RFLOW5 | S6: 50 subscribers, 240 reconnects, 0 backwards counts, 50/50 end on DB counts |
| R-AUD-1 | green | TestCompile_ValueNeverInSQL_RAUD1, TestRedTeam_SQLInjectionInert_RAUD1, **TestCount_EveryAcceptedRuleRunsOnPostgres_RAUD1** (361 accepted rules executed) | 45-case hostile corpus; 2 min fuzz, 5.9 M executions |
| R-AUD-2 | green | TestApprove_EnqueuesConsentedOnly_RAUD2, TestClaim_ConsentRevoked_Skipped_RAUD2, **TestApprove_ConsentProperty_RAUD2** (30 random rule × consent × channel rounds) | I2 = 0 in S1, S2, S7 |
| R-CNT-1 | green | TestCount_SharedVectors_RCNT1, TestRedTeam_SMSBoundaryAfterPersonalization_RCNT1, **TestApprove_SMSFitsForEveryRecipient_RCNT1** (3530 recipients, longest 159) | 300-char SMS → repair or 422 |
| R-CNT-2 | green | TestValidate_Subject_RCNT2, TestRedTeam_SubjectSingleLine_RCNT2 | same repair / 422 path |
| R-CNT-3 | green | TestRender_SharedVectors_RCNT3, TestRedTeam_HostileNamesRender_RCNT3, e2e (`{{coupon}}` blocked) | hostile seed names render literally and escaped |
| R-SND-1 | green | TestClaim_OnlySendingCampaignsAndDueMessages_RSND1 | **S3(c)**: all-user approve median 128 ms for 7542 messages, all still `queued`; S2 SIGKILL |
| R-SND-2 | green | TestLimiter_NeverMoreThanNInAnyWindow_RSND2, TestDispatcher_ColdConnections_…_RSND2, TestTakeover_…_RSND2 | I3: provider max 100 per 1 s in S1, S2, S6, S7 and live |
| R-SND-3 | green | TestApprove_IdempotentReplay_RSND3, TestLeaseExpiry_ConfirmationReplays_RSND3, TestDispatcher_ConfirmationReplay_RSND3 | I1 = 0 everywhere |
| R-SND-4 | green | TestClassify_Table_RSND4, TestBackoff_FullJitterBounds_RSND4, TestDispatcher_RetryClasses_RSND4 | S2 I6: max 7 attempts (5 + replays) |
| R-OBS-1 | green | R-FLOW-5 tests, throughput.test.ts, e2e chart | S6 |
| R-OBS-2 | green | TestWorkerMetrics_Exposed_ROBS2, TestMockProvider_MetricsExposed_ROBS2 | S1 I5: DB = provider = worker metric |
| R-OBS-3 | green | TestHealthAndReadiness_ROBS3, TestSystemStatus_StaleAndDeadWorkers_ROBS3 | S7: worker `/readyz` 503 during the DB outage, 200 after |
| R-AI-1 | green | 46 RAI1 tests incl. TestService_RepairThen422_RAI1, TestAnthropic_RequestShape_RAI1, TestParse_ProbeFixture_RAI1 (real capture) | 45-case corpus; claudecode eval; the paid API path awaits the owner's `make smoke-live` |
| R-AI-2 | green | TestNew_ProviderSelection_RAI2; `make test` runs on an `internal: true` network | CI workflow written, first run after push |
| R-RES-1 | green | TestLoad_Defaults_RRES1, TestResolvedLLMProvider_RRES1 | fresh-clone step 4 (key-only `.env`) |
| R-RES-2 | green | TestGenerate_IsDeterministic_RRES2, TestRun_IsIdempotent_RRES2 | **S5b** 100 000 users: preview p95 40 ms; fresh-clone restart keeps 5000 |
| R-RES-3 | green | TestMockProvider_FaultModes_RRES3, **TestProviderConfig_ProxyReadsAndUpdates_RRES3** | **S8**: 500 rps open loop, 4999/4999 answered, p99 296 ms |
| R-TECH-1 | green | structural (Go 1.27.3, Vue 3.5 + TS strict + Vite) | — |
| R-DEL-1 | owner | — | repository and push are the owner's (need.md N-PLAN-009) |
| R-DEL-2 | green | TestMigrate_IsIdempotentAndComplete_RDEL2 | `make fresh-clone-check` (above) |
| R-DEL-3 | green | `make test` | 97 s in a clean clone; CI after push |
| R-DEL-4 | green | README: setup with/without key, architecture, LLM design, tests, stress, eval, limitations, not done, time | — |
| R-DEL-5 | green | DECISIONS D-1…D-15 | — |
| R-DEL-6 | green | `make sanitize-check` (exports listed and present, gitleaks, personal terms) | `ai-sessions/sessions/`, `ai-sessions/subagents/` (10 transcripts), `usage/`; 00-planning export is the owner's |
