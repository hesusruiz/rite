package rite

import (
	"reflect"
	"testing"
)

func TestParseFromBytes(t *testing.T) {
	type args struct {
		fileName string
		src      []byte
	}
	tests := []struct {
		name    string
		args    args
		want    *Parser
		wantErr bool
	}{
		{
			name: "No bibliography",
			args: args{
				fileName: "text",
				src: []byte(`
---
title: Rite, a simple syntax for writing documents in HTML
editors:
   - name: "Jesus Ruiz"
     email: "hesusruiz@gmail.com"
     company: "JesusRuiz"
     url: "https://www.linkedin.com/in/jesus-ruiz-martinez/"

latestVersion: "https://hesusruiz.github.io/rite"
github: "https://github.com/hesusruiz/rite"
rite:
    norespec: true
---

<section #abstract>

    Proof of Democracy (PoD) is the consensus algorithm [[pepe]] used in Alastria RedT and RedB and in the future ISBE (Spanish Blockchain Services Infrastructure).

				`),
			},
			wantErr: false,
			want:    nil,
		},
		{
			name: "No content",
			args: args{
				fileName: "text",
				src:      []byte(""),
			},
			wantErr: true,
			want:    nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseFromBytes(tt.args.fileName, tt.args.src)
			got.RetrieveBliblioData()

			// Render to HTML
			fragmentHTML := got.RenderHTML()
			biblio := got.RenderBibliography()

			fragmentHTML = append(fragmentHTML, biblio...)

			if (err != nil) != tt.wantErr {
				t.Errorf("ParseFromBytes() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseFromBytes() = %v, want %v", got, tt.want)
			}
		})
	}
}
