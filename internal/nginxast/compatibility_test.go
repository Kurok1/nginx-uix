/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.6.0
 */
package nginxast

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompatibilityFixturesRoundTripLosslessly(t *testing.T) {
	t.Parallel()

	root := filepath.Join("..", "..", "tests", "fixtures", "nginx", "compatibility")
	fixtureCount := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".conf" {
			return nil
		}
		fixtureCount++
		relativePath, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		t.Run(relativePath, func(t *testing.T) {
			source, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile() error = %v", err)
			}
			configuration := strings.ReplaceAll(string(source), "{{PORT}}", "18080")
			document, err := Parse(configuration)
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if rendered := document.Render(); rendered != configuration {
				t.Fatalf("Render() changed fixture\nwant %q\n got %q", configuration, rendered)
			}
		})
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir() error = %v", err)
	}
	if fixtureCount != 5 {
		t.Fatalf("fixture count = %d, want 5", fixtureCount)
	}
}
