package codeintel

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/catalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func loadCatalog(t testing.TB) *catalog.Catalog {
	t.Helper()
	c, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCheckLocatesNestedProblems(t *testing.T) {
	cat := loadCatalog(t)
	src := `resource "aws_instance" "web" {
  ami           = "ami-1"
  instance_type = "t3.micro"

  root_block_device {
    volume_sizee = 10
  }

  ebs_block_device {
    device_name = "/dev/sdb"
  }
  ebs_block_device {
    volume_typo = "gp3"
  }

  dynamic "network_interface" {
    for_each = []
    content {
      device_index = 0
    }
  }

  lifecycle {
    ignore_changes = [tags]
  }
}
`
	ps := Check(cat, map[string][]byte{"main.tf": []byte(src)}, "main.tf")
	got := map[string]bool{}
	for _, p := range ps {
		got[fmt.Sprintf("%s@%d:%d", p.Code, p.Line, p.Column)] = true
	}
	for _, want := range []string{"unknown_attribute@6:5", "unknown_attribute@13:5", "missing_attribute@12:3"} {
		if !got[want] {
			t.Errorf("missing %s in %v", want, got)
		}
	}
	if len(ps) != 3 {
		t.Errorf("problems = %+v", ps)
	}
}

func TestCheckDeclarationsSkippedWhileAnotherFileIsBroken(t *testing.T) {
	cat := loadCatalog(t)
	files := map[string][]byte{
		"main.tf":  []byte("output \"o\" {\n  value = var.x\n}\n"),
		"other.tf": []byte("variable \"x\" {\n"),
	}
	if ps := Check(cat, files, "main.tf"); len(ps) != 0 {
		t.Errorf("problems = %+v", ps)
	}
	if ps := Check(cat, files, "other.tf"); len(ps) == 0 || ps[0].Severity != model.SeverityError {
		t.Errorf("broken file problems = %+v", ps)
	}
}

// BenchmarkCheck measures one as-you-type check of a 60-resource file in a
// workflow of three such files.
func BenchmarkCheck(b *testing.B) {
	cat := loadCatalog(b)
	var sb strings.Builder
	sb.WriteString("variable \"prefix\" {\n  type = string\n}\n")
	for i := range 60 {
		fmt.Fprintf(&sb, "resource \"aws_sqs_queue\" \"q%d\" {\n  name = \"${var.prefix}-%d\"\n  visibility_timeout_seconds = 30\n}\n", i, i)
		fmt.Fprintf(&sb, "resource \"aws_sns_topic_subscription\" \"s%d\" {\n  topic_arn = aws_sqs_queue.q%d.arn\n  protocol = \"sqs\"\n  endpoint = aws_sqs_queue.q%d.arn\n}\n", i, i, i)
	}
	files := map[string][]byte{"main.tf": []byte(sb.String())}
	for i := range 2 {
		files[fmt.Sprintf("more%d.tf", i)] = []byte(strings.ReplaceAll(strings.ReplaceAll(sb.String(), "\"q", fmt.Sprintf("\"m%dq", i)), "\"s", fmt.Sprintf("\"m%ds", i)))
	}
	b.ResetTimer()
	for b.Loop() {
		Check(cat, files, "main.tf")
	}
}
