# fsnotify Recursive Watches: Plansicherung und verbindliches Fencing

## Zusammenfassung

Der zuletzt bestätigte **„Strikte plattformgleiche Qualitätsplan“** wird inhaltlich unverändert im Repository gesichert. Zusätzlich wird der Fencing-Ansatz als eigener versionierter Plan gespeichert und anschließend als technische Schutzstruktur umgesetzt.

Bis diese Struktur vollständig grün validiert und mit `recursive-watch-contract-v1` eingefroren ist, werden keine Produktionsdateien der fsnotify-Backends verändert und kein Code-Audit begonnen.

## Korrigierte Produktionsbasis

- `recursive-watch-validation-v1` bleibt unverändert als historischer
  Validierungsstand auf `fce4bab` erhalten.
- Die nachträglich separat validierten Windows-, FEN- und kqueue-Korrekturen
  sind über die Pull Requests 12 und 13 in `main` enthalten.
- Die verbindliche Produktionsbasis für diesen Fencing-PR ist
  `a7b03eef28b15bef1c1b88530b117572c3d19378`.
- Der Fencing-PR darf gegenüber dieser Basis keine Produktionsdatei verändern.
- `recursive-watch-contract-v1` wird erst nach grünem PR, manuellem Merge und
  grünem Post-Merge-Lauf auf dem dann aktuellen `main`-SHA angelegt.

## Gesicherte Dokumente

Im Repository-Root werden angelegt:

- `RECURSIVE_WATCH_QUALITY_PLAN.md`
  - enthält den zuletzt bestätigten Qualitätsplan vollständig und unverändert
  - ausschließlich fsnotify
  - identischer Recursive Control Contract auf allen Backends
  - bestehende native Eventunterschiede bleiben getrennt dokumentiert
  - kein Traefik-Bezug

- `RECURSIVE_WATCH_FENCING_PLAN.md`
  - enthält diesen vollständigen Fencing- und Rollout-Plan
  - beschreibt Arbeitsregeln, Contract Freeze, CI-Gates und GitHub-Schutz

- `AGENTS.md`
  - bindende Arbeitsanweisung für weitere Codex-/Agent-Läufe

- `RECURSIVE_WATCH_CONTRACT.md`
  - normativer, nummerierter Recursive-Vertrag

- `QUALITY_AUDIT.md`
  - einzige Fortschritts- und Befundquelle während Audit und Refactoring

Fork-spezifische Plan- und Auditdateien werden später nicht automatisch Teil eines Upstream-PRs.

## Verbindliche `AGENTS.md`-Regeln

`AGENTS.md` legt fest:

- Scope ist ausschließlich `MK796/fsnotify`.
- Traefik und andere Downstreams sind ausgeschlossen.
- Vor jeder Arbeit müssen Qualitätsplan, Fencing-Plan, Contract und Audit-Ledger gelesen werden.
- Recursive Control Contract ist auf inotify, IOCP, kqueue und FEN identisch.
- Keine Produktionsänderung ohne `AUDIT-*`-Befund und zugehörige `RC-*`-Regel.
- Keine Testabschwächung, zusätzlichen Sleeps, gelockerten Erwartungen oder blinden Retries.
- Contract-Tests, Allowlist und Policy-Dateien dürfen während eines Produktionsrefactorings nicht verändert werden.
- Keine neue Runtime-Abhängigkeit.
- Kein neues exportiertes API-Symbol ohne ausdrückliche Vertragsänderung.
- Builds, Benchmarks und Plattformtests ausschließlich auf GitHub-Runners.
- Höchstens ein temporärer Remote-Branch.
- Höchstens ein Full-Matrix-Lauf gleichzeitig.
- Bei einem echten Testfehler sofort stoppen und Ursache untersuchen.
- Keine automatischen Konfliktlösungen gegen Upstream.
- Keine automatische Veröffentlichung eines PRs.
- Der Agent darf keine Vertragsfreigabe im Namen des Benutzers erzeugen.
- Abschluss erst bei null offenen `BLOCKER`- und `MAJOR`-Befunden.

## Normativer Contract

`RECURSIVE_WATCH_CONTRACT.md` verwendet mindestens folgende IDs:

- `RC-001`: Recursive Add akzeptiert einen existierenden Directory Root.
- `RC-002`: Ungültiger Root verändert keinen Zustand.
- `RC-003`: Bestehende Unterverzeichnisse werden registriert.
- `RC-004`: Neue oder hineingeschobene Teilbäume werden registriert.
- `RC-005`: Wiederholtes Add ist idempotent.
- `RC-006`: WatchList enthält nur explizite Benutzerwatches.
- `RC-007`: Interne Subwatches sind niemals öffentlich sichtbar.
- `RC-008`: Explizite und rekursive Ownership bleiben unabhängig.
- `RC-009`: Überlappende Recursive Roots bleiben unabhängig.
- `RC-010`: Remove unterstützt Root und Root-plus-`/...` äquivalent.
- `RC-011`: Remove erhält weiterhin besessene innere Watches.
- `RC-012`: Root-Löschung aktualisiert WatchList und Ownership.
- `RC-013`: Rename innerhalb des Baums erhält Recursive Coverage.
- `RC-014`: Move-out entfernt ausschließlich nicht mehr benötigte Watches.
- `RC-015`: Move-in registriert den vollständigen Teilbaum.
- `RC-016`: Gleicher Pfad mit neuer Objektidentität wird neu registriert.
- `RC-017`: Fehlgeschlagenes Add wird atomar zurückgerollt.
- `RC-018`: Remove, Rollback und Close hinterlassen keine Ressourcen.
- `RC-019`: Gleichzeitiges Add, Remove, WatchList und Close ist race- und deadlock-frei.
- `RC-020`: Mehrfaches Close ist sicher.
- `RC-021`: Backpressure erzeugt keinen permanenten Lifecycle-Deadlock.
- `RC-022`: Interne Recursive-Verwaltung erzeugt keine synthetischen öffentlichen Events.
- `RC-023`: Recursive Watching führt keine neuen Plattformunterschiede ein.
- `RC-024`: Native Eventunterschiede benötigen einen nicht-rekursiven Nachweis.
- `RC-025`: Fehlerklassen und Zustandswirkungen sind plattformgleich.
- `RC-026`: Ownership verwendet echte Pfadkomponentengrenzen.
- `RC-027`: Nicht-rekursive API und Semantik bleiben kompatibel.

Jede Regel enthält `MUST`, `MUST NOT` oder `MAY`, öffentlich beobachtbares Verhalten, zugehörige Tests, betroffene Backends und erlaubte native Einschränkungen.

## Audit-Ledger

`QUALITY_AUDIT.md` besitzt strukturierte Einträge:

```text
ID:
Severity:
Status:
Contract:
Backend:
Finding:
Evidence:
Decision:
Fix commit:
Validation runs:
```

ID-Präfixe:

- `AUDIT-COMMON-*`
- `AUDIT-INOTIFY-*`
- `AUDIT-WIN-*`
- `AUDIT-KQUEUE-*`
- `AUDIT-FEN-*`
- `AUDIT-TEST-*`
- `AUDIT-CI-*`
- `AUDIT-PERF-*`

Severity:

- `BLOCKER`
- `MAJOR`
- `MINOR`

Status:

- `OPEN`
- `IN_PROGRESS`
- `RESOLVED`
- `ACCEPTED_NATIVE_EVENT`
- `ACCEPTED_NATIVE_CAPABILITY`
- `ACCEPTED_TEST_ENVIRONMENT`

Für Recursive-Control-Abweichungen existiert kein `ACCEPTED`-Status.

## Plattform-Allowlist und Metatest

Eine testinterne, maschinenlesbare Allowlist wird eingeführt. Jeder Eintrag enthält:

- eindeutige Ausnahme-ID
- Testfall
- Plattform oder Backend-Gruppe
- Kategorie
- technische Begründung
- nicht-rekursiven Reproduktionstest
- Dokumentationsverweis

Erlaubte Kategorien:

- `NATIVE_EVENT`
- `NATIVE_CAPABILITY`
- `TEST_ENVIRONMENT`

Ein Policy-Metatest prüft:

- keine GOOS-Zweige in der gemeinsamen Recursive Contract Suite
- keine Skips in Control-Contract-Tests
- jeder GOOS-spezifische Recursive-Output steht in der Allowlist
- jeder Recursive-Skip steht in der Allowlist
- jeder Allowlist-Eintrag besitzt Begründung und Nachweis
- ungenutzte Allowlist-Einträge schlagen fehl
- neue nicht klassifizierte Plattformabweichungen schlagen fehl
- WatchList-, Ownership-, Rollback-, Remove-, Cleanup- und Close-Unterschiede können nicht allowlisted werden

## Contract Freeze

Nach vollständiger Contract- und Testprüfung wird angelegt:

```text
recursive-watch-contract-v1
```

Der Tag zeigt auf den Commit mit:

- Qualitätsplan
- Fencing-Plan
- `AGENTS.md`
- normativem Contract
- Audit-Ledger-Struktur
- gemeinsamer Contract Suite
- Plattform-Allowlist
- Policy-Metatest
- grünen Fencing-Workflows

Danach gelten als eingefroren:

- `RECURSIVE_WATCH_CONTRACT.md`
- gemeinsame Contract-Tests
- Plattform-Allowlist
- Policy-Metatest
- zentrale Contract-Testsemantik
- Fencing-relevante `AGENTS.md`-Regeln

Eine spätere Vertragsänderung:

- erfolgt in einem eigenen PR
- enthält keinen Produktionscode
- nennt betroffene `RC-*`-Regeln
- dokumentiert die Benutzerfreigabe
- erzeugt einen neuen monotonen Contract-Tag
- wird niemals zusammen mit dem daraus folgenden Produktionsfix committed

## CI-Fencing

Ein eigener Policy-Workflow wird auf Pull Requests und manuelle Ausführung aktiviert.

Eindeutige Required-Check-Namen:

- `policy`
- `stock-test-gate`
- `staticcheck`
- `recursive-contract`
- `recursive-backend-integration`
- `api-compatibility`
- `resource-invariants`

### `policy`

Prüft:

- `AGENTS.md` und Planreferenzen vorhanden
- Produktionsänderungen nennen `AUDIT-*` und `RC-*`
- keine neue Runtime-Abhängigkeit
- keine gemischte Produktions- und Contract-Änderung
- keine Veränderung eingefrorener Dateien während Refactorings
- keine unbekannte Plattformausnahme
- keine Contract-Skips oder GOOS-Zweige
- leerer `gofmt`-Diff
- Commit-Serie enthält keine absichtlich roten Zwischenstände

### `stock-test-gate`

- aggregiert alle bestehenden Upstream-Testjobs unter einem eindeutigen Check
- wird nur grün, wenn sämtliche Stock-Plattformjobs erfolgreich sind

### `recursive-contract`

- führt dieselbe öffentliche Contract Suite auf allen vier Backend-Gruppen aus
- erlaubt keine Control-Contract-Ausnahme

### `recursive-backend-integration`

- behält vollständige Backend-, Race-, Stress- und Lifecycle-Tests
- erhält eine globale Concurrency-Gruppe
- `cancel-in-progress: false`
- verhindert parallele Full-Matrix-Läufe

### `api-compatibility`

- führt `apidiff` gegen die festgelegte Upstream-Basis aus
- akzeptiert keine inkompatible exportierte Änderung
- verwendete Toolversion wird unveränderlich gepinnt
- verändert `go.mod` nicht

### `resource-invariants`

- prüft interne Watch-, Owner- und Ressourcenbestände
- validiert Add, Rollback, Remove und Close
- schlägt bei verbleibenden ausschließlich rekursiven Ressourcen fehl

## Schutz gegen Testanpassung

CI vergleicht jeden Refactor-PR mit `recursive-watch-contract-v1`.

Ein PR schlägt fehl, wenn er gleichzeitig:

- Produktionsdateien und eingefrorene Contract-Dateien verändert
- Produktionsdateien und Plattform-Allowlist verändert
- Produktionsdateien und zentrale Contract-Erwartungen verändert
- eine neue Ausnahme oder einen neuen Skip einführt
- eine Wartezeit erhöht, ohne dass ausschließlich Testinfrastruktur geändert wird

Ein fehlgeschlagener Contract-Test darf nur durch Produktionscode korrigiert werden. Eine Vertragskorrektur benötigt den getrennten Contract-Change-Prozess.

## Commit-Regeln

Fencing-Aufbau erfolgt in dieser Reihenfolge:

1. `docs: record recursive watch quality and fencing plans`
2. `docs: define recursive watch contract and audit ledger`
3. `test: enforce recursive platform exception policy`
4. `test: establish cross-platform recursive contract`
5. `ci: enforce recursive watch governance`
6. `docs: freeze recursive watch contract v1`

Keiner dieser Commits verändert Produktionscode.

Spätere Produktionscommits enthalten im Committext:

```text
Contract: RC-...
Audit: AUDIT-...
```

Ein Commit verändert genau eine Verantwortung.

## GitHub-Branch-Schutz

Aktuell ist `main` ungeschützt. Branch Protection wird erst aktiviert, nachdem alle neuen Checks mindestens einmal erfolgreich existiert haben.

Danach:

- Pull Request für jede Änderung an `main` erforderlich
- Required Checks gemäß obiger Liste
- Branch muss vor Merge aktuell sein
- Conversation Resolution erforderlich
- lineare Historie erforderlich
- Force-Push deaktiviert
- Branch-Löschung deaktiviert
- Schutz gilt auch für Administratoren
- kein automatischer Merge
- finale Merge-Entscheidung bleibt manuell beim Benutzer

Die erstmalige Fencing-Einführung erfolgt ebenfalls über einen einzelnen temporären PR. Nach Merge und Schutzaktivierung wird der Arbeitsbranch gelöscht.

## Ausführungsreihenfolge

1. Ausgangszustand und Referenz-Tag passiv verifizieren.
2. Einzigen Branch `quality/recursive-watch-v1` erzeugen.
3. Qualitätsplan wortgetreu speichern.
4. Fencing-Plan wortgetreu speichern.
5. `AGENTS.md`, Contract und Audit-Ledger ergänzen.
6. Allowlist und Policy-Metatest implementieren.
7. Gemeinsame Recursive Contract Suite vervollständigen.
8. CI-Gates und eindeutige Aggregator-Checks einrichten.
9. Alle Stock- und Fencing-Checks ausführen.
10. Echte Fehler einzeln beheben; keine blinden Wiederholungen.
11. Fencing-PR nach `main` übernehmen.
12. `recursive-watch-contract-v1` auf dem grünen Main-SHA setzen.
13. Branch Protection aktivieren.
14. Remote-Branch entfernen.
15. Endzustand prüfen:
    - sauberer Worktree
    - nur `main`
    - beide Referenz-Tags vorhanden
    - sämtliche Required Checks grün
    - kein Produktionscode verändert
16. Erst danach mit Phase 1 des Qualitätsplans fortfahren.

## Abnahmekriterien

Das Fencing ist abgeschlossen, wenn:

- beide Pläne versioniert vorhanden sind
- `AGENTS.md` jede spätere Agentenarbeit bindet
- alle `RC-*`-Regeln dokumentiert sind
- Audit-IDs und Zustände festgelegt sind
- Contract Suite keine GOOS-Zweige oder Skips enthält
- jede verbleibende native Ausnahme allowlisted und belegt ist
- Policy-CI gemischte Code-/Contract-Änderungen blockiert
- Branch Protection aktiv ist
- Force-Push und direkte Main-Änderungen blockiert sind
- `recursive-watch-contract-v1` auf einem vollständig grünen SHA existiert
- Produktionscode gegenüber `a7b03eef` unverändert ist
- keine temporären Branches oder unnötigen Actions-Läufe verbleiben

## Annahmen

- Der vom Benutzer eingefügte Qualitätsplan wird ohne inhaltliche Neuinterpretation gespeichert.
- Dieser Fencing-Plan wird ebenfalls vollständig gespeichert.
- Fencing schützt vor unbeabsichtigtem Scope Drift und Testanpassungen; ein GitHub-Administrator mit Repository-Zugang könnte Schutz technisch ändern, weshalb manuelle Merge-Kontrolle zusätzlich bestehen bleibt.
- Keine neue Runtime-Abhängigkeit wird aufgenommen.
- Traefik und andere Downstreams bleiben ausgeschlossen.
- Der Code-Audit beginnt erst nach erfolgreichem Contract Freeze und GitHub-Schutz.
