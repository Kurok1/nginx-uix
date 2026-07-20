/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	ManifestSchemaVersion uint16 = 1
	manifestMagic                = "NUXM"
)

// Entry is one safe, deterministic filesystem record in a manifest.
type Entry struct {
	Path           RelativePath
	Type           EntryType
	Class          EntryClass
	Mode           fs.FileMode
	Size           int64
	ContentDigest  Digest
	SafeLinkTarget RelativePath
}

// Manifest is the canonical structure and managed-content identity of a root.
type Manifest struct {
	SchemaVersion uint16
	PolicyVersion uint16
	Entries       []Entry
	Dependencies  []Dependency
	EntryCount    int
	ManagedBytes  int64
}

// Validate verifies schema, canonical fields, duplicates, and resource bounds.
func (m Manifest) Validate(limits Limits) error {
	if m.SchemaVersion != ManifestSchemaVersion {
		return errors.New("validate manifest: unsupported schema version")
	}
	if m.PolicyVersion != NewPolicy().Version() {
		return errors.New("validate manifest: unsupported policy version")
	}
	if limits.MaxEntries <= 0 || len(m.Entries) > limits.MaxEntries || m.EntryCount != len(m.Entries) {
		return fmt.Errorf("validate manifest: entries: %w", ErrLimitExceeded)
	}
	if limits.MaxIncludeEdges <= 0 || len(m.Dependencies) > limits.MaxIncludeEdges {
		return fmt.Errorf("validate manifest: dependencies: %w", ErrLimitExceeded)
	}

	paths := make(map[RelativePath]struct{}, len(m.Entries))
	var managedBytes int64
	for _, entry := range m.Entries {
		if err := validateManifestEntry(entry, limits); err != nil {
			return err
		}
		if _, duplicate := paths[entry.Path]; duplicate {
			return errors.New("validate manifest: duplicate entry path")
		}
		paths[entry.Path] = struct{}{}
		if entry.Class == EntryManagedText {
			if entry.Size > math.MaxInt64-managedBytes {
				return fmt.Errorf("validate manifest: managed bytes: %w", ErrLimitExceeded)
			}
			managedBytes += entry.Size
		}
	}
	if limits.MaxManagedBytes < 0 || managedBytes > limits.MaxManagedBytes || m.ManagedBytes != managedBytes {
		return fmt.Errorf("validate manifest: managed bytes: %w", ErrLimitExceeded)
	}

	edges := make(map[string]struct{}, len(m.Dependencies))
	for _, dependency := range m.Dependencies {
		canonical, err := canonicalDependency(dependency, limits)
		if err != nil {
			return err
		}
		key, err := marshalDependencyRecord(canonical)
		if err != nil {
			return err
		}
		if _, duplicate := edges[string(key)]; duplicate {
			return errors.New("validate manifest: duplicate dependency")
		}
		edges[string(key)] = struct{}{}
	}
	return nil
}

// MarshalBinary returns the versioned canonical manifest representation.
func (m Manifest) MarshalBinary() ([]byte, error) {
	if err := m.Validate(DefaultLimits()); err != nil {
		return nil, err
	}
	entries := slices.Clone(m.Entries)
	slices.SortFunc(entries, compareEntries)
	dependencies := slices.Clone(m.Dependencies)
	for index, dependency := range dependencies {
		canonical, err := canonicalDependency(dependency, DefaultLimits())
		if err != nil {
			return nil, err
		}
		dependencies[index] = canonical
	}
	slices.SortFunc(dependencies, compareDependencies)

	var payload bytes.Buffer
	payload.WriteString(manifestMagic)
	writeUint16(&payload, m.SchemaVersion)
	writeUint16(&payload, m.PolicyVersion)
	entryCount, err := boundedUint32(int64(len(entries)))
	if err != nil {
		return nil, err
	}
	managedBytes, err := nonNegativeUint64(m.ManagedBytes)
	if err != nil {
		return nil, err
	}
	dependencyCount, err := boundedUint32(int64(len(dependencies)))
	if err != nil {
		return nil, err
	}
	writeUint32(&payload, entryCount)
	writeUint64(&payload, managedBytes)
	writeUint32(&payload, dependencyCount)
	for _, entry := range entries {
		record, err := marshalEntryRecord(entry)
		if err != nil {
			return nil, err
		}
		if err := writeRecord(&payload, record); err != nil {
			return nil, err
		}
	}
	for _, dependency := range dependencies {
		record, err := marshalDependencyRecord(dependency)
		if err != nil {
			return nil, err
		}
		if err := writeRecord(&payload, record); err != nil {
			return nil, err
		}
	}
	return payload.Bytes(), nil
}

// ParseManifest parses and validates one complete canonical manifest payload.
func ParseManifest(payload []byte, limits Limits) (Manifest, error) {
	if len(payload) > maximumManifestPayload(limits) {
		return Manifest{}, fmt.Errorf("parse manifest: payload: %w", ErrLimitExceeded)
	}
	reader := bytes.NewReader(payload)
	magic := make([]byte, len(manifestMagic))
	if _, err := io.ReadFull(reader, magic); err != nil || string(magic) != manifestMagic {
		return Manifest{}, errors.New("parse manifest: invalid magic")
	}
	schemaVersion, err := readUint16(reader)
	if err != nil {
		return Manifest{}, fmt.Errorf("parse manifest schema: %w", err)
	}
	policyVersion, err := readUint16(reader)
	if err != nil {
		return Manifest{}, fmt.Errorf("parse manifest policy: %w", err)
	}
	if schemaVersion != ManifestSchemaVersion || policyVersion != NewPolicy().Version() {
		return Manifest{}, errors.New("parse manifest: unsupported version")
	}
	entryCount, err := readUint32(reader)
	if err != nil {
		return Manifest{}, fmt.Errorf("parse manifest entry count: %w", err)
	}
	managedBytes, err := readUint64(reader)
	if err != nil || managedBytes > math.MaxInt64 {
		return Manifest{}, errors.New("parse manifest: invalid managed bytes")
	}
	dependencyCount, err := readUint32(reader)
	if err != nil {
		return Manifest{}, fmt.Errorf("parse manifest dependency count: %w", err)
	}
	if limits.MaxEntries <= 0 || uint64(entryCount) > uint64(limits.MaxEntries) ||
		limits.MaxIncludeEdges <= 0 || uint64(dependencyCount) > uint64(limits.MaxIncludeEdges) {
		return Manifest{}, fmt.Errorf("parse manifest: records: %w", ErrLimitExceeded)
	}

	manifest := Manifest{
		SchemaVersion: schemaVersion,
		PolicyVersion: policyVersion,
		Entries:       make([]Entry, 0, entryCount),
		Dependencies:  make([]Dependency, 0, dependencyCount),
		EntryCount:    int(entryCount),
		ManagedBytes:  int64(managedBytes),
	}
	for range entryCount {
		record, err := readRecord(reader)
		if err != nil {
			return Manifest{}, fmt.Errorf("parse manifest entry: %w", err)
		}
		entry, err := parseEntryRecord(record, limits)
		if err != nil {
			return Manifest{}, err
		}
		manifest.Entries = append(manifest.Entries, entry)
	}
	for range dependencyCount {
		record, err := readRecord(reader)
		if err != nil {
			return Manifest{}, fmt.Errorf("parse manifest dependency: %w", err)
		}
		dependency, err := parseDependencyRecord(record, limits)
		if err != nil {
			return Manifest{}, err
		}
		manifest.Dependencies = append(manifest.Dependencies, dependency)
	}
	if reader.Len() != 0 {
		return Manifest{}, errors.New("parse manifest: trailing bytes")
	}
	if err := manifest.Validate(limits); err != nil {
		return Manifest{}, err
	}
	if !slices.IsSortedFunc(manifest.Entries, compareEntries) || !slices.IsSortedFunc(manifest.Dependencies, compareDependencies) {
		return Manifest{}, errors.New("parse manifest: records are not canonical")
	}
	return manifest, nil
}

// Digest returns the SHA-256 identity of a valid canonical manifest.
func (m Manifest) Digest() Digest {
	payload, err := m.MarshalBinary()
	if err != nil {
		return Digest{}
	}
	return Digest(sha256.Sum256(payload))
}

// Entry returns the record for one exact relative path.
func (m Manifest) Entry(path RelativePath) (Entry, bool) {
	for _, entry := range m.Entries {
		if entry.Path == path {
			return entry, true
		}
	}
	return Entry{}, false
}

func validateManifestEntry(entry Entry, limits Limits) error {
	if _, err := ParseRelativePath(string(entry.Path), limits); err != nil {
		return fmt.Errorf("validate manifest entry path: %w", err)
	}
	if entry.Size < 0 || entry.Mode&^fs.ModePerm != 0 {
		return errors.New("validate manifest entry: invalid metadata")
	}
	if entry.ContentDigest != (Digest{}) && entry.Class != EntryManagedText {
		return errors.New("validate manifest entry: unmanaged content digest")
	}
	if entry.Class == EntryManagedText && entry.ContentDigest == (Digest{}) {
		return errors.New("validate manifest entry: missing managed content digest")
	}
	if entry.Class == EntryManagedText && (limits.MaxFileBytes < 0 || entry.Size > limits.MaxFileBytes) {
		return fmt.Errorf("validate manifest entry: file bytes: %w", ErrLimitExceeded)
	}

	switch entry.Type {
	case EntryRegular:
		if entry.SafeLinkTarget != "" || !isRegularClass(entry.Class) {
			return errors.New("validate manifest entry: invalid regular classification")
		}
	case EntryDirectory:
		if entry.Class != EntryDirectoryReadOnly || entry.Mode != 0 || entry.Size != 0 || entry.ContentDigest != (Digest{}) || entry.SafeLinkTarget != "" {
			return errors.New("validate manifest entry: invalid directory classification")
		}
	case EntrySymlink:
		if entry.Mode != 0 || entry.Size != 0 || entry.ContentDigest != (Digest{}) || !isSymlinkClass(entry.Class) {
			return errors.New("validate manifest entry: invalid symlink classification")
		}
		if entry.Class == EntrySymlinkInternal {
			if _, err := ParseRelativePath(string(entry.SafeLinkTarget), limits); err != nil {
				return fmt.Errorf("validate manifest link target: %w", err)
			}
		} else if entry.SafeLinkTarget != "" {
			return errors.New("validate manifest entry: unsafe symlink target")
		}
	case EntrySpecial:
		if entry.Class != EntrySpecialReadOnly || entry.Mode != 0 || entry.Size != 0 || entry.ContentDigest != (Digest{}) || entry.SafeLinkTarget != "" {
			return errors.New("validate manifest entry: invalid special classification")
		}
	default:
		return errors.New("validate manifest entry: invalid entry type")
	}
	return nil
}

func isRegularClass(class EntryClass) bool {
	switch class {
	case EntryManagedText, EntrySensitiveMaterial, EntryNotCandidate, EntryInvalidText, EntryFileLimit:
		return true
	case EntryDirectoryReadOnly, EntrySymlinkInternal, EntrySymlinkExternal, EntrySymlinkUnavailable, EntrySpecialReadOnly:
		return false
	}
	return false
}

func isSymlinkClass(class EntryClass) bool {
	return class == EntrySymlinkInternal || class == EntrySymlinkExternal || class == EntrySymlinkUnavailable
}

func canonicalDependency(dependency Dependency, limits Limits) (Dependency, error) {
	if _, err := ParseRelativePath(string(dependency.Source), limits); err != nil {
		return Dependency{}, fmt.Errorf("validate manifest dependency source: %w", err)
	}
	if dependency.Line <= 0 || dependency.Column <= 0 || dependency.Line > math.MaxUint32 || dependency.Column > math.MaxUint32 {
		return Dependency{}, errors.New("validate manifest dependency: invalid location")
	}
	if !utf8.ValidString(dependency.DisplayValue) || len(dependency.DisplayValue) > limits.MaxPathBytes {
		return Dependency{}, errors.New("validate manifest dependency: invalid display value")
	}
	switch dependency.Status {
	case DependencyExternal:
		if dependency.Target != "" || (dependency.DisplayValue != "external" && dependency.DisplayValue != "[external]") || dependency.Cycle {
			return Dependency{}, errors.New("validate manifest dependency: invalid external edge")
		}
		dependency.DisplayValue = "external"
	case DependencyUnresolved:
		if dependency.Target != "" || (dependency.DisplayValue != "unresolved" && dependency.DisplayValue != "[unresolved]") || dependency.Cycle {
			return Dependency{}, errors.New("validate manifest dependency: invalid unresolved edge")
		}
		dependency.DisplayValue = "unresolved"
	case DependencyResolved, DependencyMissing, DependencySymlink, DependencySpecial, DependencyCycle:
		if _, err := ParseRelativePath(dependency.DisplayValue, limits); err != nil {
			return Dependency{}, fmt.Errorf("validate manifest dependency display: %w", err)
		}
		if _, err := ParseRelativePath(string(dependency.Target), limits); err != nil {
			return Dependency{}, fmt.Errorf("validate manifest dependency target: %w", err)
		}
		if (dependency.Status == DependencyCycle) != dependency.Cycle {
			return Dependency{}, errors.New("validate manifest dependency: invalid cycle edge")
		}
	default:
		return Dependency{}, errors.New("validate manifest dependency: invalid status")
	}
	return dependency, nil
}

func marshalEntryRecord(entry Entry) ([]byte, error) {
	var record bytes.Buffer
	if err := writeString(&record, string(entry.Path)); err != nil {
		return nil, err
	}
	record.WriteByte(entryTypeCode(entry.Type))
	record.WriteByte(entryClassCode(entry.Class))
	writeUint32(&record, uint32(entry.Mode.Perm()))
	size, err := nonNegativeUint64(entry.Size)
	if err != nil {
		return nil, err
	}
	writeUint64(&record, size)
	if entry.Class == EntryManagedText {
		record.WriteByte(1)
		record.Write(entry.ContentDigest[:])
	} else {
		record.WriteByte(0)
	}
	if err := writeString(&record, string(entry.SafeLinkTarget)); err != nil {
		return nil, err
	}
	return record.Bytes(), nil
}

func parseEntryRecord(record []byte, limits Limits) (Entry, error) {
	reader := bytes.NewReader(record)
	rawPath, err := readString(reader, limits.MaxPathBytes)
	if err != nil {
		return Entry{}, fmt.Errorf("parse manifest entry path: %w", err)
	}
	path, err := ParseRelativePath(rawPath, limits)
	if err != nil {
		return Entry{}, err
	}
	typeCode, err := reader.ReadByte()
	if err != nil {
		return Entry{}, err
	}
	classCode, err := reader.ReadByte()
	if err != nil {
		return Entry{}, err
	}
	mode, err := readUint32(reader)
	if err != nil || mode > uint32(fs.ModePerm) {
		return Entry{}, errors.New("parse manifest entry: invalid mode")
	}
	size, err := readUint64(reader)
	if err != nil || size > math.MaxInt64 {
		return Entry{}, errors.New("parse manifest entry: invalid size")
	}
	hasDigest, err := reader.ReadByte()
	if err != nil || hasDigest > 1 {
		return Entry{}, errors.New("parse manifest entry: invalid digest flag")
	}
	var digest Digest
	if hasDigest == 1 {
		if _, err := io.ReadFull(reader, digest[:]); err != nil {
			return Entry{}, fmt.Errorf("parse manifest entry digest: %w", err)
		}
	}
	rawTarget, err := readString(reader, limits.MaxPathBytes)
	if err != nil {
		return Entry{}, fmt.Errorf("parse manifest entry target: %w", err)
	}
	if reader.Len() != 0 {
		return Entry{}, errors.New("parse manifest entry: trailing record bytes")
	}
	entry := Entry{
		Path:           path,
		Type:           entryTypeFromCode(typeCode),
		Class:          entryClassFromCode(classCode),
		Mode:           fs.FileMode(mode),
		Size:           int64(size),
		ContentDigest:  digest,
		SafeLinkTarget: RelativePath(rawTarget),
	}
	if err := validateManifestEntry(entry, limits); err != nil {
		return Entry{}, err
	}
	return entry, nil
}

func marshalDependencyRecord(dependency Dependency) ([]byte, error) {
	var record bytes.Buffer
	for _, value := range []string{string(dependency.Source), dependency.DisplayValue, string(dependency.Target)} {
		if err := writeString(&record, value); err != nil {
			return nil, err
		}
	}
	line, err := boundedUint32(int64(dependency.Line))
	if err != nil {
		return nil, err
	}
	column, err := boundedUint32(int64(dependency.Column))
	if err != nil {
		return nil, err
	}
	writeUint32(&record, line)
	writeUint32(&record, column)
	record.WriteByte(dependencyStatusCode(dependency.Status))
	if dependency.Cycle {
		record.WriteByte(1)
	} else {
		record.WriteByte(0)
	}
	return record.Bytes(), nil
}

func parseDependencyRecord(record []byte, limits Limits) (Dependency, error) {
	reader := bytes.NewReader(record)
	values := make([]string, 3)
	for index := range values {
		value, err := readString(reader, limits.MaxPathBytes)
		if err != nil {
			return Dependency{}, fmt.Errorf("parse manifest dependency string: %w", err)
		}
		values[index] = value
	}
	line, err := readUint32(reader)
	if err != nil {
		return Dependency{}, err
	}
	column, err := readUint32(reader)
	if err != nil {
		return Dependency{}, err
	}
	status, err := reader.ReadByte()
	if err != nil {
		return Dependency{}, err
	}
	cycle, err := reader.ReadByte()
	if err != nil || cycle > 1 || reader.Len() != 0 {
		return Dependency{}, errors.New("parse manifest dependency: invalid record")
	}
	dependency := Dependency{
		Source:       RelativePath(values[0]),
		DisplayValue: values[1],
		Target:       RelativePath(values[2]),
		Line:         int(line),
		Column:       int(column),
		Status:       dependencyStatusFromCode(status),
		Cycle:        cycle == 1,
	}
	return canonicalDependency(dependency, limits)
}

func compareEntries(left, right Entry) int {
	return bytes.Compare([]byte(left.Path), []byte(right.Path))
}

func compareDependencies(left, right Dependency) int {
	if comparison := bytes.Compare([]byte(left.Source), []byte(right.Source)); comparison != 0 {
		return comparison
	}
	if comparison := left.Line - right.Line; comparison != 0 {
		return comparison
	}
	if comparison := left.Column - right.Column; comparison != 0 {
		return comparison
	}
	if comparison := bytes.Compare([]byte(left.Target), []byte(right.Target)); comparison != 0 {
		return comparison
	}
	if comparison := strings.Compare(string(left.Status), string(right.Status)); comparison != 0 {
		return comparison
	}
	return strings.Compare(left.DisplayValue, right.DisplayValue)
}

func writeRecord(writer *bytes.Buffer, record []byte) error {
	length, err := boundedUint32(int64(len(record)))
	if err != nil {
		return fmt.Errorf("marshal manifest record: %w", err)
	}
	writeUint32(writer, length)
	writer.Write(record)
	return nil
}

func readRecord(reader *bytes.Reader) ([]byte, error) {
	length, err := readUint32(reader)
	if err != nil {
		return nil, err
	}
	if int64(length) > int64(reader.Len()) {
		return nil, io.ErrUnexpectedEOF
	}
	record := make([]byte, length)
	_, err = io.ReadFull(reader, record)
	return record, err
}

func writeString(writer *bytes.Buffer, value string) error {
	length, err := boundedUint32(int64(len(value)))
	if err != nil {
		return fmt.Errorf("marshal manifest string: %w", err)
	}
	writeUint32(writer, length)
	writer.WriteString(value)
	return nil
}

func readString(reader *bytes.Reader, limit int) (string, error) {
	length, err := readUint32(reader)
	if err != nil {
		return "", err
	}
	if limit < 0 || int64(length) > int64(limit) {
		return "", fmt.Errorf("parse manifest string: %w", ErrLimitExceeded)
	}
	if int64(length) > int64(reader.Len()) {
		return "", io.ErrUnexpectedEOF
	}
	value := make([]byte, length)
	if _, err := io.ReadFull(reader, value); err != nil {
		return "", err
	}
	if !utf8.Valid(value) {
		return "", errors.New("parse manifest string: invalid utf-8")
	}
	return string(value), nil
}

func boundedUint32(value int64) (uint32, error) {
	if value < 0 || value > math.MaxUint32 {
		return 0, ErrLimitExceeded
	}
	return uint32(value), nil
}

func nonNegativeUint64(value int64) (uint64, error) {
	if value < 0 {
		return 0, ErrLimitExceeded
	}
	return uint64(value), nil
}

func writeUint16(writer *bytes.Buffer, value uint16) {
	var raw [2]byte
	binary.BigEndian.PutUint16(raw[:], value)
	writer.Write(raw[:])
}

func writeUint32(writer *bytes.Buffer, value uint32) {
	var raw [4]byte
	binary.BigEndian.PutUint32(raw[:], value)
	writer.Write(raw[:])
}

func writeUint64(writer *bytes.Buffer, value uint64) {
	var raw [8]byte
	binary.BigEndian.PutUint64(raw[:], value)
	writer.Write(raw[:])
}

func readUint16(reader io.Reader) (uint16, error) {
	var raw [2]byte
	if _, err := io.ReadFull(reader, raw[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(raw[:]), nil
}

func readUint32(reader io.Reader) (uint32, error) {
	var raw [4]byte
	if _, err := io.ReadFull(reader, raw[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(raw[:]), nil
}

func readUint64(reader io.Reader) (uint64, error) {
	var raw [8]byte
	if _, err := io.ReadFull(reader, raw[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(raw[:]), nil
}

func maximumManifestPayload(limits Limits) int {
	if limits.MaxEntries < 0 || limits.MaxIncludeEdges < 0 || limits.MaxPathBytes < 0 {
		return 0
	}
	entryBytes := int64(limits.MaxEntries) * int64(64+2*limits.MaxPathBytes)
	dependencyBytes := int64(limits.MaxIncludeEdges) * int64(32+3*limits.MaxPathBytes)
	total := int64(24) + entryBytes + dependencyBytes
	if total < 0 || total > int64(math.MaxInt) {
		return math.MaxInt
	}
	return int(total)
}

func entryTypeCode(value EntryType) byte {
	switch value {
	case EntryRegular:
		return 1
	case EntryDirectory:
		return 2
	case EntrySymlink:
		return 3
	case EntrySpecial:
		return 4
	default:
		return 0
	}
}

func entryTypeFromCode(value byte) EntryType {
	switch value {
	case 1:
		return EntryRegular
	case 2:
		return EntryDirectory
	case 3:
		return EntrySymlink
	case 4:
		return EntrySpecial
	default:
		return EntryType("")
	}
}

func entryClassCode(value EntryClass) byte {
	classes := []EntryClass{
		EntryManagedText, EntrySensitiveMaterial, EntryNotCandidate, EntryInvalidText, EntryFileLimit,
		EntryDirectoryReadOnly, EntrySymlinkInternal, EntrySymlinkExternal, EntrySymlinkUnavailable, EntrySpecialReadOnly,
	}
	for index, class := range classes {
		if value == class {
			return byte(index + 1)
		}
	}
	return 0
}

func entryClassFromCode(value byte) EntryClass {
	classes := []EntryClass{
		EntryManagedText, EntrySensitiveMaterial, EntryNotCandidate, EntryInvalidText, EntryFileLimit,
		EntryDirectoryReadOnly, EntrySymlinkInternal, EntrySymlinkExternal, EntrySymlinkUnavailable, EntrySpecialReadOnly,
	}
	if value == 0 || int(value) > len(classes) {
		return EntryClass("")
	}
	return classes[value-1]
}

func dependencyStatusCode(value DependencyStatus) byte {
	statuses := []DependencyStatus{
		DependencyResolved, DependencyMissing, DependencyExternal, DependencyUnresolved, DependencySymlink, DependencySpecial, DependencyCycle,
	}
	for index, status := range statuses {
		if value == status {
			return byte(index + 1)
		}
	}
	return 0
}

func dependencyStatusFromCode(value byte) DependencyStatus {
	statuses := []DependencyStatus{
		DependencyResolved, DependencyMissing, DependencyExternal, DependencyUnresolved, DependencySymlink, DependencySpecial, DependencyCycle,
	}
	if value == 0 || int(value) > len(statuses) {
		return DependencyStatus("")
	}
	return statuses[value-1]
}
