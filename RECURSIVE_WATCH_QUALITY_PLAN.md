# fsnotify Recursive Watches: Strikter plattformgleicher Qualitätsplan

## Zusammenfassung

Der Recursive-Vertrag wird auf Linux/inotify, Windows/IOCP, macOS/BSD/kqueue und illumos/FEN **identisch** definiert und getestet. Kein Backend darf bei Add, Remove, WatchList, Ownership, dynamischer Registrierung, Rename-Abdeckung, Rollback, Cleanup, Backpressure oder Close abweichen.

Die bereits vor Recursive Watching existierenden Unterschiede des allgemeinen fsnotify-Eventstroms bleiben erhalten. Sie werden strikt von Recursive-Semantik getrennt, einzeln belegt und dürfen keine Recursive-Implementierungslücke verdecken.

Der validierte Stand `fce4bab` bleibt über `recursive-watch-validation-v1` unveränderlich erhalten. Traefik und andere Downstreams sind vollständig ausgeschlossen.

## Aktuelle Behandlung von Plattformunterschieden

fsnotify behandelt Unterschiede derzeit auf mehreren Ebenen:

- Jedes Betriebssystem verwendet ein eigenes Backend mit Build Tags.
- `README.md` und `fsnotify.go` dokumentieren native Unterschiede:
  - Linux kann beim Entfernen offener Dateien `Chmod` vor `Remove` senden.
  - Windows sendet kein `Chmod`.
  - Windows kann beim Löschen eines Verzeichnisbaums Kind-Events auslassen.
  - Windows kann zusätzliche Directory-`Write`-Events liefern.
  - Overflow-Unterstützung unterscheidet sich.
- Der Script-Runner unterstützt unterschiedliche erwartete Outputs:
  - exakter `GOOS`, beispielsweise `windows:`
  - Backend-Gruppen `kqueue:` und `fen:`
  - allgemeiner Default-Output
- `cmpEvents` sortiert Events vor dem Vergleich; die Reihenfolge wird aktuell grundsätzlich nicht geprüft.
- `require` und `skip` überspringen Tests abhängig von Betriebssystem, Capability oder Berechtigung.
- `supportsRecurse()` aktiviert Recursive-Tests nur auf unterstützten Backends.
- `supportsFilter()` erlaubt Filtertests aktuell nur unter Linux.
- `supportsRename()` behandelt `RenamedFrom` nur auf Linux und Windows als verfügbar.
- Recursive-Scripte enthalten bereits GOOS-spezifische Eventerwartungen.
- Einzelne Recursive-Fälle werden noch vollständig übersprungen, beispielsweise bestimmte Windows-Rename-Szenarien.
- `VALIDATION.md` spricht bisher von „platform-equivalent“ statt von einem identischen Recursive-Vertrag.

Das ist als historische Testinfrastruktur sinnvoll, aber aktuell zu permissiv: Native Eventunterschiede, fehlende Backend-Fähigkeiten und echte Recursive-Implementierungsfehler sind nicht streng genug voneinander getrennt.

## Verbindliche Klassifikation

Jeder Unterschied wird künftig genau einer Kategorie zugeordnet:

### 1. Recursive Control Contract

Muss auf allen Backends identisch sein. Keine GOOS-Zweige und keine Skips erlaubt.

Dazu gehören:

- Add und wiederholtes Add
- vorhandene und neu erzeugte Unterverzeichnisse
- dynamische Subtree-Registrierung
- explizite und rekursive Ownership
- überlappende Roots
- Remove
- WatchList
- Rename- und Move-Abdeckung
- Rollback
- Ressourcenfreigabe
- Backpressure
- Close und parallele Lifecycle-Operationen

### 2. Native Event Representation

Darf sich nur unterscheiden, wenn derselbe Unterschied bereits bei nicht-rekursiven Watches existiert.

Beispiele:

- zusätzliches Directory-`Write` unter Windows
- `Chmod` statt sofortigem `Remove` unter Linux
- fehlendes `RenamedFrom` auf einem nativen Backend
- Event-Coalescing
- fehlende Kind-Events bei nativer Verzeichnislöschung
- backendabhängiges Overflow-Signal

### 3. Native Capability

Darf sich unterscheiden, wenn fsnotify dies bereits öffentlich über `Supports()` oder Dokumentation ausweist.

Beispiele:

- unportable Open-, Read- und Close-Events
- Eventfilter, sofern das Backend sie allgemein nicht unterstützt
- native Overflow-Unterstützung

### 4. Testumgebung

Ein Skip ist nur zulässig, wenn die Operation wegen Runner-, Berechtigungs- oder Dateisystembedingungen nicht ausgeführt werden kann.

Beispiele:

- fehlende Windows-Symlink-Berechtigung
- fehlendes `mknod`
- fehlender Race Detector auf der Plattform

Eine Implementierungslücke darf niemals als Testumgebungsproblem klassifiziert werden.

## Exakter Recursive API-Vertrag

Auf allen unterstützten Backends muss identisch gelten:

- `Add(filepath.Join(root, "..."))` aktiviert Recursive Watching.
- Der Root muss ein vorhandenes Verzeichnis sein.
- Add einer Datei oder eines nicht vorhandenen Roots schlägt fehl und verändert keinen Zustand.
- OS-spezifische Errnos dürfen variieren; Erfolg, Fehlerklasse und Zustandswirkung nicht.
- Bereits vorhandene Unterverzeichnisse werden vollständig aufgenommen.
- Später erzeugte oder hineingeschobene Teilbäume werden vollständig aufgenommen.
- Wiederholtes Add desselben Recursive Roots ist idempotent.
- `WatchList()` enthält nur explizite Benutzerwatches.
- Recursive Roots erscheinen ohne `/...`.
- Interne Subwatches erscheinen niemals.
- Explizite Child-Watches überleben das Entfernen eines äußeren Recursive Roots.
- Innere Recursive Roots überleben das Entfernen eines äußeren Recursive Roots.
- `Remove(root)` und `Remove(filepath.Join(root, "..."))` sind äquivalent.
- Remove löscht alle ausschließlich diesem Root gehörenden internen Watches.
- Automatisches Verschwinden eines Roots entfernt ihn aus `WatchList()`.
- Rename innerhalb des Baums erhält Recursive Coverage.
- Move aus dem Baum entfernt nicht mehr benötigte interne Watches.
- Move in den Baum registriert den vollständigen neuen Teilbaum.
- Ein neues Dateisystemobjekt am gleichen Pfad wird nicht mit dem alten Objekt verwechselt.
- Teilweise fehlgeschlagene Registrierung wird atomar zurückgerollt.
- Nach Rollback, Remove und Close verbleiben keine internen Owners oder OS-Ressourcen.
- Gleichzeitiges Add, Remove, WatchList und Close darf weder Race noch Deadlock erzeugen.
- Mehrfaches Close ist sicher.
- Blockierte Consumer dürfen keinen permanenten Lifecycle-Deadlock erzeugen.
- Recursive Watching darf keine zusätzlichen öffentlichen Events allein aufgrund interner Subwatch-Verwaltung erzeugen.

## Phase 1: Referenz und Auditstruktur

1. Referenz-Tag und SHA verifizieren.
2. Genau einen temporären Branch `quality/recursive-watch-v1` verwenden.
3. Keine parallelen Experimentbranches und keine parallelen Full Matrices.
4. Alle Builds und Plattformtests ausschließlich auf GitHub-Runners ausführen.
5. Ein fork-internes Audit-Dokument führen mit:
   - Referenz-SHA
   - Upstream-Basis
   - Vertragsmatrix
   - Plattformausnahmen
   - Audit-Befunden
   - Benchmarkwerten
   - Ressourcenwerten
   - Entscheidungen
6. Befundstatus:
   - `OPEN`
   - `RESOLVED`
   - `ACCEPTED-NATIVE-EVENT`
   - `ACCEPTED-NATIVE-CAPABILITY`
   - `ACCEPTED-TEST-ENVIRONMENT`
7. Für Recursive Control Contract existiert kein `ACCEPTED`-Status; Abweichungen müssen behoben werden.

## Phase 2: Plattformausnahmen vollständig inventarisieren

- Alle GOOS-spezifischen Output-Blöcke unter `testdata/watch-recurse` erfassen.
- Alle `skip`- und `require`-Direktiven erfassen.
- Alle `runtime.GOOS`-Zweige in Recursive-Tests erfassen.
- Alle backend-spezifischen Recursive-Erwartungen erfassen.
- Für jeden Unterschied einen nicht-rekursiven Reproduktionstest suchen oder erstellen.
- Native Unterschiede nur akzeptieren, wenn sie ohne Recursive Watching reproduzierbar sind.
- Unterschiede ohne nicht-rekursiven Nachweis als Recursive-Bug klassifizieren.
- Bestehende Skips einzeln überprüfen; kein bestehender Skip wird automatisch übernommen.
- Der Windows-Skip beim Rename eines Ancestors mit separat beobachtetem Descendant bleibt `OPEN`, bis bewiesen ist, ob OS oder Implementierung verantwortlich ist.
- Unterschiedliche WatchList-, Ownership-, Remove-, Rollback- oder Cleanup-Ergebnisse sind immer `BLOCKER`.

## Phase 3: Automatischer Ausnahme-Guard

Eine testinterne Allowlist für Recursive-Plattformausnahmen einführen:

- Schlüssel: Testfall plus Plattform oder Backend-Gruppe
- Kategorie: Native Event, Native Capability oder Test Environment
- technische Begründung
- Verweis auf nicht-rekursiven Reproduktionstest
- Verweis auf bestehende öffentliche Dokumentation, sofern vorhanden

Ein Metatest durchsucht Recursive-Scripte und öffentliche Recursive-Tests:

- jeder GOOS-spezifische Output muss in der Allowlist stehen
- jeder Skip muss in der Allowlist stehen
- Control-Contract-Tests dürfen keine Allowlist verwenden
- nicht mehr benötigte Einträge lassen den Test fehlschlagen
- neue Plattformausnahmen lassen CI ohne vorherige Klassifikation fehlschlagen

Die Allowlist ist Testinfrastruktur und verändert keine Produktions-API.

## Phase 4: Gemeinsame Contract-Suite

Eine öffentliche, backendunabhängige `TestRecursiveContract`-Suite aufbauen.

Eigenschaften:

- keine `runtime.GOOS`-Zweige
- keine Backend-Type-Assertions
- keine plattformspezifischen Skips
- ausschließlich öffentliche `Watcher`-Methoden
- dieselben Eingaben und Zustandsassertions auf jedem Backend
- zustandsbasierte Assertions statt exakter nativer Eventfolgen
- bounded eventual assertions statt pauschal längerer Sleeps
- WatchList-, Ownership- und Cleanup-Wirkung exakt prüfen
- Recursive Coverage nach Rename oder Move durch nachfolgende Sentinel-Operationen bestätigen
- zusätzliche native Events ignorieren, aber erforderliche semantische Beobachtung verlangen

Die Suite enthält mindestens:

- leerer und vorbefüllter Root
- dynamische Verzeichnisse
- `mkdir -p`
- idempotentes Add
- expliziter Child-Watch
- überlappende Roots
- innerer Recursive Root
- Remove in beiden akzeptierten Schreibweisen
- Root-Löschung
- Rename und Move
- Austausch gleicher Pfade
- Registrierungs-Rollback
- WatchList
- paralleles Add/Remove
- Close und doppeltes Close
- Backpressure

## Phase 5: Native Event-Suite

Die bestehende Script-Infrastruktur bleibt für den tatsächlichen Eventstrom erhalten.

Regeln:

- Default-Output beschreibt die gemeinsame Eventrepräsentation.
- Plattformblöcke enthalten ausschließlich allowlist-validierte native Unterschiede.
- Eventreihenfolge bleibt nur dann unspezifiziert, wenn fsnotify sie allgemein nicht garantiert.
- Wo Reihenfolge Teil der dokumentierten Semantik ist, darf `cmpEvents` nicht durch Sortieren darüber hinweggehen.
- Rename-, Remove-, Chmod- und Directory-Write-Unterschiede werden getrennt von Recursive Ownership getestet.
- Backend-Gruppen wie `kqueue:` oder `fen:` werden nur verwendet, wenn jede Plattform der Gruppe separat validiert wurde.
- Recursive Tests dürfen keine neuen nativen Unterschiede gegenüber äquivalenten nicht-rekursiven Tests erzeugen.

## Phase 6: Tests nach Upstream-Regeln sortieren

- Gemeinsamer Vertrag nach `testdata/watch-recurse` oder `fsnotify_test.go`.
- Backend-Testdateien ausschließlich für native Interna:
  - inotify Move-Cookies und Descriptor-Ownership
  - Windows IOCP- und Overlapped-Lifecycle
  - kqueue Descriptor-, Rebase- und Link-Zustände
  - FEN One-Shot-Association und Rearm
- Keine gemeinsame Vertragsaussage bleibt ausschließlich in einem Backend-Test.
- Keine Erwartung wird abgeschwächt.
- Keine Wartezeit wird pauschal erhöht.
- Kein Retry kaschiert Flakiness.
- Ein Backend-Test wird nur entfernt, wenn der gemeinsame Test die externe Semantik abdeckt und keine interne Invariante verloren geht.
- Reine Teststrukturänderungen werden vor Produktionsrefactorings validiert.

## Phase 7: Line-by-Line-Audit

Reihenfolge:

1. öffentliche API und Pfadhelfer
2. inotify
3. Windows/IOCP
4. kqueue
5. FEN

Prüfpunkte:

- einheitliches Ownership-Modell
- identische Zustandsübergänge
- Lock-Reihenfolge
- Map-Zugriffe
- Add-, Remove-, Rename-, Move- und Close-Pfade
- vollständige Rollbacks
- Objektidentität
- Freigabe aller OS-Ressourcen
- parallele Operationen
- Tree-Walks innerhalb von Locks
- vollständige Map-Scans
- potenziell quadratische Algorithmen
- unnötige Kopien und `Stat`-Aufrufe
- verständliche OS-spezifische Kommentare

Befundklassen:

- `BLOCKER`: falsche externe Semantik, Race, Deadlock, Leak, Panic oder API-Bruch
- `MAJOR`: unvollständiger Rollback, unbeschränkte Komplexität oder relevante Performanceprobleme
- `MINOR`: Benennung, lokale Duplikation, Kommentare oder Struktur

Kein Refactoring beginnt vor abgeschlossenem Audit.

## Phase 8: API-Kompatibilität

- `apidiff` gegen die Upstream-Basis ausführen.
- Null inkompatible exportierte API-Änderungen verlangen.
- Zusätzlich manuell prüfen:
  - nicht-rekursives Add und Remove
  - WatchList
  - Close und Channels
  - bestehende Sentinel-Fehler
  - `WithOps`
  - `WithBufferSize`
  - `Supports`
- Keine neuen exportierten Symbole.
- Keine Signaturänderung.
- Keine Änderung bestehender nicht-rekursiver Semantik.
- Exakte Error-Strings sind kein Vertrag; dokumentierte Fehlerklassen und Zustandswirkung sind es.

## Phase 9: Performance- und Ressourcenbaseline

Referenz und Kandidat im selben GitHub-Actions-Lauf vergleichen:

- bestehender `BenchmarkWatch`
- bestehender `BenchmarkAddRemove`
- Recursive Add/Remove mit 1, 100 und 1.000 Verzeichnissen
- vorbefüllter Baum
- dynamisch wachsender Baum
- überlappende Roots
- vollständiger Rollback
- vollständiges Remove und Close
- `ns/op`, `B/op`, `allocs/op`
- mindestens 20 Wiederholungen
- Auswertung mit `benchstat`

Ressourcenprüfungen:

- explizite Roots
- interne Owners
- physische Watches
- offene Backend-Ressourcen
- Zustand nach Rollback
- Zustand nach Remove
- Zustand nach Close
- keine verbleibende Recursive-Goroutine

Gates:

- keine ungeklärte Erhöhung von `B/op` oder `allocs/op`
- signifikante Laufzeitregression über 10 Prozent ist `MAJOR`
- Regression zwischen 5 und 10 Prozent wird untersucht und erneut gemessen
- `pprof` oder `trace` nur bei konkretem Performancebefund
- kein dauerhaftes zeitbasiertes Merge-Gate auf schwankenden Hosted Runnern

## Phase 10: Differential- und State-Machine-Prüfung

- Referenz und Kandidat in getrennten Worktrees mit identischen Operationsfolgen ausführen.
- Recursive Control Contract exakt vergleichen.
- Native Events anhand der Allowlist vergleichen.
- WatchList, Fehlerklassen und Ressourcen vergleichen.
- Keine Produktions-Normalisierung einführen.
- Testnormalisierung entfernt ausschließlich bekannte native Eventrepräsentationen.
- Deterministische State-Machine-Sequenzen mit festen Seeds verwenden.
- Go-Fuzzing dauerhaft nur für reine Pfad-, Präfix- und Ownership-Helfer.
- Live-Dateisystem-Fuzzing und Mutation Testing bleiben optionale Diagnosewerkzeuge.

## Phase 11: Kontrollierte Vereinfachung

- Erst nach Vertrag, Ausnahmeaudit, Teststruktur, Codeaudit und Baseline beginnen.
- Nur semantisch identische Logik zusammenführen.
- Native Event- und Ressourcenmechanik backend-spezifisch lassen.
- Keine Abstraktion nur zur Reduktion der Zeilenzahl.
- Pro Commit genau eine Verantwortung.
- Zu jedem Refactoring gehören:
  - Audit-Befund
  - unveränderte Contract-Suite
  - Differential-Vergleich
  - Race-Test
  - Ressourcenvergleich
  - Benchmarkvergleich
- Jede Verschlechterung von Semantik, Reviewbarkeit oder Messwerten verwirft das Refactoring.

## Phase 12: Upstream-Commit-Serie

Vom aktuellen Upstream-Main rekonstruieren:

1. plattformgleiche Contract-Suite und Ausnahme-Guard
2. gemeinsame Pfad- und Ownership-Korrekturen
3. inotify-Korrekturen
4. Windows-IOCP-Korrekturen
5. kqueue-Implementierung
6. FEN-Implementierung
7. öffentliche `/...`-Aktivierung und Dokumentation
8. minimale notwendige CI-Erweiterung

Jeder Commit:

- kompiliert eigenständig
- besteht die für seinen Stand gültigen Tests
- enthält keine absichtlich rote Zwischenstufe
- besitzt eine begrenzte Verantwortung
- erhält ursprüngliche Autorenschaft und PR-Verweise

Fork-Auditberichte, Diagnoseworkflows und extreme Wiederholungsläufe werden nicht automatisch Teil des Upstream-Diffs.

## Phase 13: Finale Validierung

Auf einem unveränderten Kandidaten-SHA:

- leerer `gofmt`-Diff
- `go vet`
- Staticcheck
- `apidiff`
- `govulncheck`
- vollständiges `go test ./...`
- Race Detector, wo unterstützt
- Standard- und Buffer-Varianten
- gemeinsame Recursive Contract-Suite auf jedem Backend
- Ausnahme-Guard
- native Event-Suite
- Backend-Lifecycle-Tests
- Backpressure, Overflow, Stress und Exhaustion
- Differential- und State-Machine-Prüfung
- Ressourcenbaseline
- Benchmarkvergleich
- drei sequenzielle Full Matrices
- keine Source-Änderung oder Job-Retry zwischen den Matrizen
- jeder echte Testfehler setzt die Dreierserie zurück
- Runner-Provisionierungsfehler werden dokumentiert und zählen nicht als Erfolg

## Abnahmekriterien

Der Kandidat ist upstream-reif, wenn:

- Recursive Control Contract auf allen Backends identisch ist
- kein GOOS-Zweig oder Skip im Control Contract existiert
- jede verbleibende Plattformabweichung als bestehendes natives Event-, Capability- oder Testumgebungsverhalten belegt ist
- keine Recursive-spezifische Plattformausnahme offen ist
- WatchList, Ownership, Remove, Rollback, Cleanup und Close überall gleich funktionieren
- keine `BLOCKER`- oder `MAJOR`-Befunde offen sind
- alle `MINOR`-Befunde behoben oder nachvollziehbar verworfen wurden
- keine inkompatible API-Änderung besteht
- keine ungeklärte Ressourcen- oder Performanceverschlechterung besteht
- alle Backend-Gruppen vergleichbar validiert sind
- drei finale Full Matrices bestanden wurden
- die Commit-Serie klein, buildbar, attribuiert und reviewbar ist
- keine temporären Branches oder unnötigen Artefakte verbleiben

## Annahmen

- Plattformgleichheit gilt strikt für Recursive API, Control State und Lifecycle.
- Bereits bestehende native Unterschiede des allgemeinen Eventstroms bleiben erhalten.
- Recursive Watching darf keine neuen Plattformunterschiede erzeugen.
- Produktionscode enthält keine Event-Normalisierungsschicht.
- Traefik und alle anderen Downstreams sind ausgeschlossen.
- Keine neue Runtime-Abhängigkeit wird aufgenommen.
- Alle Plattformtests und Benchmarks laufen auf GitHub-Runners.
- Während der Arbeit existiert höchstens ein temporärer Remote-Branch.
- `recursive-watch-validation-v1` bleibt dauerhaft als Referenz bestehen.
