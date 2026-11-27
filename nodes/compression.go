package nodes

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"mime"
	"path"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// The Compression node's server-owned binding and type.
const (
	CompressionExecutorID = "core.compression"
	CompressionNodeType   = "kilasflow.compression"
)

// The Compression node's operations and formats, as n8n names them.
const (
	CompressionCompress   = "compress"
	CompressionDecompress = "decompress"
	CompressionZip        = "zip"
	CompressionGzip       = "gzip"
)

// The limits that keep a hostile archive from filling the engine's memory: one
// file's inflated size, the whole archive's, and how many files it may hold.
const (
	compressionMaxFile    = 64 << 20
	compressionMaxTotal   = 128 << 20
	compressionMaxEntries = 1000
	compressionMaxInput   = 64 << 20
)

func compressionNode() node.Definition {
	compress := []node.VisibilityCondition{{Key: "operation", Equals: CompressionCompress}}
	return node.Definition{
		Type:        CompressionNodeType,
		Version:     workflow.V(1),
		DisplayName: "Compression",
		Description: "Compresses files into a ZIP archive or gzip files, or unpacks ZIP and gzip files, in the item's binary data.",
		Category:    "Transform",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "builtin:file-archive"},
		IconColor:   "#a16207",
		Subtitle:    "{{ $parameter.operation }}",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "operation", Label: "Operation", Kind: node.PropertyOptions, Required: true, Default: CompressionDecompress,
				Options: []node.PropertyOption{
					{Label: "Compress", Value: CompressionCompress},
					{Label: "Decompress", Value: CompressionDecompress},
				},
			},
			{
				Key: "binaryPropertyName", Label: "Input Binary Field(s)", Kind: node.PropertyString, Default: "data",
				Description: "The binary field(s) on the item that hold the files. Several fields are separated by commas.",
			},
			{
				Key: "outputFormat", Label: "Output Format", Kind: node.PropertyOptions, Default: CompressionZip, VisibleWhen: compress,
				Options: []node.PropertyOption{
					{Label: "Gzip", Value: CompressionGzip},
					{Label: "Zip", Value: CompressionZip},
				},
				Description: "Zip packs every input file into one archive. Gzip compresses each file on its own.",
			},
			{
				Key: "fileName", Label: "File Name", Kind: node.PropertyString, Default: "", VisibleWhen: compress,
				Description: "The archive's file name when the format is zip. Empty names it data.zip.",
			},
			{
				Key: "binaryPropertyOutput", Label: "Put Output File in Field", Kind: node.PropertyString, Default: "data", VisibleWhen: compress,
				Description: "The binary field the zip archive is written to.",
			},
			{
				Key: "outputPrefix", Label: "Output Prefix", Kind: node.PropertyString, Default: "file_",
				Description: "Decompress names each unpacked file's binary field this prefix plus a number from 0. Gzip compress does the same with its own output.",
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     CompressionExecutorID,
		Validate:       validateCompressionConfiguration,
	}
}

func validateCompressionConfiguration(n workflow.Node) error {
	if !expression.IsExpression(n.Parameters["operation"]) {
		switch operation := textValue(n.Parameters["operation"], CompressionDecompress); operation {
		case CompressionCompress, CompressionDecompress:
		default:
			return fmt.Errorf("operation must be compress or decompress, not %q", operation)
		}
	}
	if !expression.IsExpression(n.Parameters["outputFormat"]) {
		switch format := textValue(n.Parameters["outputFormat"], CompressionZip); format {
		case CompressionZip, CompressionGzip:
		default:
			return fmt.Errorf("output format must be zip or gzip, not %q", format)
		}
	}
	return nil
}

// compressionFile is one file read from, or bound for, binary storage.
type compressionFile struct {
	name      string
	mediaType string
	contents  []byte
}

func executeCompression(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	items := input["main"]
	if len(items) == 0 {
		return workflow.NodeOutput{[]workflow.Item{}}, nil
	}
	if request.Binaries == nil {
		return nil, fmt.Errorf("node %q: binary storage is not configured on this install", ir.Name)
	}
	out := make([]workflow.Item, 0, len(items))
	for index, item := range items {
		parameters, err := expression.Resolve(ir.Parameters, expressionContext(item, input, request, index))
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}
		built, err := compressItem(ctx, parameters, item, request.Binaries)
		if err != nil {
			return nil, fmt.Errorf("node %q: item %d: %w", ir.Name, index+1, err)
		}
		out = append(out, built)
	}
	return workflow.NodeOutput{out}, nil
}

func compressItem(ctx context.Context, parameters map[string]any, item workflow.Item, store engine.BinaryStore) (workflow.Item, error) {
	names := compressionPropertyNames(parameters["binaryPropertyName"])
	files := make([]compressionFile, 0, len(names))
	for _, name := range names {
		reference, found := item.Binary[name]
		if !found || reference.ID == "" {
			return workflow.Item{}, fmt.Errorf("the item has no binary field %q", name)
		}
		body, meta, err := store.Get(reference.ID)
		if err != nil {
			return workflow.Item{}, err
		}
		contents, readErr := io.ReadAll(io.LimitReader(body, compressionMaxInput+1))
		_ = body.Close()
		if readErr != nil {
			return workflow.Item{}, fmt.Errorf("binary field %q could not be read: %w", name, readErr)
		}
		if len(contents) > compressionMaxInput {
			return workflow.Item{}, fmt.Errorf("binary field %q is larger than %d bytes", name, compressionMaxInput)
		}
		fileName := defaultFileName(meta.FileName, reference.FileName, name)
		files = append(files, compressionFile{name: fileName, mediaType: defaultFileName(meta.MediaType, reference.MediaType, ""), contents: contents})
	}

	var produced map[string]compressionFile
	var order []string
	var err error
	operation := textValue(parameters["operation"], CompressionDecompress)
	prefix := textValue(parameters["outputPrefix"], "file_")
	switch operation {
	case CompressionDecompress:
		produced, order, err = decompressFiles(ctx, files, prefix)
	case CompressionCompress:
		produced, order, err = compressFiles(files, parameters)
	default:
		return workflow.Item{}, fmt.Errorf("operation must be compress or decompress, not %q", operation)
	}
	if err != nil {
		return workflow.Item{}, err
	}

	built := workflow.Item{JSON: cloneMap(item.JSON), Binary: map[string]workflow.BinaryRef{}}
	if item.Paired != nil {
		paired := *item.Paired
		built.Paired = &paired
	}
	for _, property := range order {
		file := produced[property]
		reference, putErr := store.Put(file.name, file.mediaType, bytes.NewReader(file.contents))
		if putErr != nil {
			return workflow.Item{}, fmt.Errorf("store %s: %w", file.name, putErr)
		}
		built.Binary[property] = reference
	}
	return built, nil
}

func defaultFileName(candidates ...string) string {
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) != "" {
			return candidate
		}
	}
	return ""
}

// compressionPropertyNames splits the comma-separated input field list.
func compressionPropertyNames(raw any) []string {
	names := make([]string, 0)
	for _, part := range strings.Split(textValue(raw, "data"), ",") {
		if name := strings.TrimSpace(part); name != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		names = append(names, "data")
	}
	return names
}

func compressFiles(files []compressionFile, parameters map[string]any) (map[string]compressionFile, []string, error) {
	format := textValue(parameters["outputFormat"], CompressionZip)
	switch format {
	case CompressionGzip:
		produced := map[string]compressionFile{}
		order := make([]string, 0, len(files))
		prefix := textValue(parameters["outputPrefix"], "file_")
		for index, file := range files {
			var packed bytes.Buffer
			writer := gzip.NewWriter(&packed)
			// The gzip header carries Latin-1 text only; a name outside it is
			// left out of the header, and the output file keeps its own.
			if name := path.Base(file.name); isLatin1(name) {
				writer.Name = name
			}
			if _, err := writer.Write(file.contents); err != nil {
				return nil, nil, err
			}
			if err := writer.Close(); err != nil {
				return nil, nil, err
			}
			property := fmt.Sprintf("%s%d", prefix, index)
			produced[property] = compressionFile{name: file.name + ".gz", mediaType: "application/gzip", contents: packed.Bytes()}
			order = append(order, property)
		}
		return produced, order, nil
	case CompressionZip:
		var packed bytes.Buffer
		writer := zip.NewWriter(&packed)
		seen := map[string]int{}
		for _, file := range files {
			entryName := uniqueEntryName(path.Base(strings.ReplaceAll(file.name, `\`, "/")), seen)
			entry, err := writer.Create(entryName)
			if err != nil {
				return nil, nil, err
			}
			if _, err := entry.Write(file.contents); err != nil {
				return nil, nil, err
			}
		}
		if err := writer.Close(); err != nil {
			return nil, nil, err
		}
		property := textValue(parameters["binaryPropertyOutput"], "data")
		name := textValue(parameters["fileName"], "data.zip")
		return map[string]compressionFile{property: {name: name, mediaType: "application/zip", contents: packed.Bytes()}}, []string{property}, nil
	default:
		return nil, nil, fmt.Errorf("output format must be zip or gzip, not %q", format)
	}
}

// uniqueEntryName keeps two files of one name apart inside an archive.
func uniqueEntryName(name string, seen map[string]int) string {
	if name == "" || name == "." {
		name = "file"
	}
	count := seen[name]
	seen[name] = count + 1
	if count == 0 {
		return name
	}
	extension := path.Ext(name)
	return fmt.Sprintf("%s-%d%s", strings.TrimSuffix(name, extension), count+1, extension)
}

// decompressFiles unpacks each input as a ZIP archive or a gzip stream, told
// apart by the format's own first bytes rather than by what the file claims to
// be, and numbers the files across all inputs.
func decompressFiles(ctx context.Context, files []compressionFile, prefix string) (map[string]compressionFile, []string, error) {
	produced := map[string]compressionFile{}
	order := make([]string, 0)
	var total int64
	add := func(file compressionFile) error {
		if len(order) >= compressionMaxEntries {
			return fmt.Errorf("the archive holds more than %d files", compressionMaxEntries)
		}
		total += int64(len(file.contents))
		if total > compressionMaxTotal {
			return fmt.Errorf("the unpacked files are larger than %d bytes together", compressionMaxTotal)
		}
		property := fmt.Sprintf("%s%d", prefix, len(order))
		produced[property] = file
		order = append(order, property)
		return nil
	}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		switch {
		case bytes.HasPrefix(file.contents, []byte("PK\x03\x04")) || bytes.HasPrefix(file.contents, []byte("PK\x05\x06")):
			reader, err := zip.NewReader(bytes.NewReader(file.contents), int64(len(file.contents)))
			if err != nil {
				return nil, nil, fmt.Errorf("%s is not a readable ZIP archive: %w", file.name, err)
			}
			for _, entry := range reader.File {
				if entry.FileInfo().IsDir() {
					continue
				}
				contents, err := readZipEntry(entry)
				if err != nil {
					return nil, nil, err
				}
				// The entry's directory is dropped: the name only labels the
				// binary field's file, and a path in it would be read as one.
				name := path.Base(strings.ReplaceAll(entry.Name, `\`, "/"))
				if err := add(compressionFile{name: name, mediaType: mediaTypeByName(name), contents: contents}); err != nil {
					return nil, nil, err
				}
			}
		case bytes.HasPrefix(file.contents, []byte{0x1f, 0x8b}):
			reader, err := gzip.NewReader(bytes.NewReader(file.contents))
			if err != nil {
				return nil, nil, fmt.Errorf("%s is not a readable gzip file: %w", file.name, err)
			}
			contents, err := io.ReadAll(io.LimitReader(reader, compressionMaxFile+1))
			if err != nil {
				return nil, nil, fmt.Errorf("%s could not be unpacked: %w", file.name, err)
			}
			if len(contents) > compressionMaxFile {
				return nil, nil, fmt.Errorf("%s unpacks to more than %d bytes", file.name, compressionMaxFile)
			}
			name := strings.TrimSuffix(path.Base(file.name), ".gz")
			if reader.Name != "" {
				name = path.Base(reader.Name)
			}
			if err := add(compressionFile{name: name, mediaType: mediaTypeByName(name), contents: contents}); err != nil {
				return nil, nil, err
			}
		default:
			return nil, nil, fmt.Errorf("%s is neither a ZIP archive nor a gzip file", file.name)
		}
	}
	return produced, order, nil
}

func readZipEntry(entry *zip.File) ([]byte, error) {
	if entry.UncompressedSize64 > compressionMaxFile {
		return nil, fmt.Errorf("%s unpacks to more than %d bytes", entry.Name, compressionMaxFile)
	}
	opened, err := entry.Open()
	if err != nil {
		return nil, fmt.Errorf("%s could not be opened: %w", entry.Name, err)
	}
	defer opened.Close()
	// The declared size is the archive's claim, so the read is bounded too.
	contents, err := io.ReadAll(io.LimitReader(opened, compressionMaxFile+1))
	if err != nil {
		return nil, fmt.Errorf("%s could not be unpacked: %w", entry.Name, err)
	}
	if len(contents) > compressionMaxFile {
		return nil, fmt.Errorf("%s unpacks to more than %d bytes", entry.Name, compressionMaxFile)
	}
	return contents, nil
}

func mediaTypeByName(name string) string {
	if found := mime.TypeByExtension(strings.ToLower(path.Ext(name))); found != "" {
		return found
	}
	return "application/octet-stream"
}

func isLatin1(text string) bool {
	for _, character := range text {
		if character > 0xFF {
			return false
		}
	}
	return true
}
