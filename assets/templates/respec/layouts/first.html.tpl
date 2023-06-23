{{- define "first_part" -}}
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="utf-8" />
    <title>{{ .Config.title }}</title>
    <meta name="viewport" content="width=device-width, initial-scale=1" />

    <!-- Opengraph Facebook Meta Tags -->
    <meta property="og:title" content="{{ .Config.title }}">
    <meta property="og:type" content="{{or .Config.og_type "website" }}">
    {{- with .Config.og_description }}
    <meta property="og:description" content="{{.}}">
    {{- end}}
    {{- with .Config.og_site_name }}
    <meta property="og:site_name" content="{{.}}">
    {{- end}}
    {{- if .Config.og_url }}
    <meta property="og:url" content="{{ .Config.og_url }}">
    {{- else if .Config.latestVersion}}
    <meta property="og:url" content="{{.Config.latestVersion}}">
    {{- end}}
    {{- with .Config.og_image }}
    <meta property="og:image" content="{{.}}">
    {{- end}}

    <script src="https://www.w3.org/Tools/respec/respec-w3c" async="" class="remove"></script>
    <script class="remove">
        var respecConfig = {
            {{- with .Config.latestVersion }}
            latestVersion: "{{.}}",
            {{- end}}
            {{- with .Config.specStatus }}
            specStatus: "{{.}}",
            {{- end}}
            {{- with .Config.edDraftURI }}
            edDraftURI: "{{.}}",
            {{- end}}
            {{- with .Config.github }}
            github: "{{.}}",
            {{- end}}
            {{- with .Config.editors }}
            editors: [
            {{- range . }}
                {
                {{- range $index, $element := .}}
                    {{ $index }}: "{{ $element }}",
                {{- end}}
                },
            {{- end}}
            ],
            {{- end }}

            {{- with .Config.authors }}
            authors: [
            {{- range . }}
                {
                {{- range $index, $element := .}}
                    {{ $index }}: "{{ $element }}",
                {{- end}}
                },
            {{- end}}
            ],
            {{- end }}

            {{- with .Biblio }}
            localBiblio: {
            {{- range $refName, $refObject := . }}
                "{{$refName}}": {
                    {{- range $index, $element := $refObject}}
                        {{ $index }}: "{{ $element }}",
                    {{- end}}
                },
            {{- end}}
            },
            {{- end}}
        };
    </script>
    <style>
        .xnotet {
            width:100%;
            display: inline-block;
            margin: 0;
            border-left: 0.5em solid #52e052;
            border-right: 6px solid #e9fbe9;
            border-top: 6px solid #e9fbe9;
            border-bottom: 6px solid #e9fbe9;
        
            overflow: auto;
            page-break-inside: avoid;
        }
        .xnotea {
            padding-left: 0.5em;
            padding-right: 0.5em;
        }
        .xwarnt {
            width:100%;
            display: inline-block;
            margin: 0;
            border-left: 0.5em solid #f11;
            border-right: 6px solid #fbe9e9;
            border-top: 6px solid #fbe9e9;
            border-bottom: 6px solid #fbe9e9;
        
            overflow: auto;
            page-break-inside: avoid;
        }
        .xwarna {
            padding-left: 0.5em;
            padding-right: 0.5em;
        }
        .xnotep {
            font-weight: bold;
        }
        code {
            color: red;
        }
        pre.nohighlight {
            padding: 0.5em;
            margin: 0;
        }
        .codecolor {
            width:100%;
            display: inline-block;
            margin: 0;
            box-shadow: 5px 5px 5px rgba(68, 68, 68, 0.6);
            border-radius: 7px;
            border: 1px solid black;
        }
        div.nohighlight {
            padding: 0.5em
        }
        ul.plain {
            list-style: none;
        }
        ul.plain li {
            text-indent: -1em;
        }
        ul.plain li>div {
            text-indent:0;
        }
        ul.plain li>p {
            text-indent:0;
        }
    </style>
</head>

<body>
    <p class="copyright">
    {{- with .Config.copyright }}
        {{ . }}
    {{- else}}
        Copyright © 2021 the document editors/authors. Text is available under the
        <a rel="license" href="https://creativecommons.org/licenses/by/4.0/legalcode">
        Creative Commons Attribution 4.0 International Public License</a>
    {{- end}}
    </p>
{{end}}
