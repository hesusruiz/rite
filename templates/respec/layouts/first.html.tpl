{{define "first_part"}}
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="utf-8" />
    <title>{{ .Config.title }}</title>
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <script src="https://www.w3.org/Tools/respec/respec-w3c" async="" class="remove"></script>
    <script class="remove">
        var respecConfig = {
            latestVersion: "https://github.com/hesusruiz/did-method-elsi",
            editors: [
                {
                    name: "Jesus Ruiz",
                    email: "hesusruiz@gmail.com",
                    company: "JesusRuiz",
                    companyURL: "https://hesusruiz.github.io/hesusruiz",
                },
            ],
            authors: [
                {
                    name: "Jesus Ruiz",
                    email: "hesusruiz@gmail.com",
                    company: "JesusRuiz",
                    companyURL: "https://hesusruiz.github.io/hesusruiz",
                },
                {
                    name: "Alejandro Nieto",
                    email: "alejandro.nieto@madisonmk.com",
                    company: "DigitelTS",
                    companyURL: "https://digitelts.es/",
                },
            ],
            github: "https://github.com/hesusruiz/did-method-elsi",

            localBiblio: {
                "ETSI-CERTOVERVIEW": {
                    title: "ETSI EN 319 412-1 V1.4.2 (2020-07) - Electronic Signatures and Infrastructures (ESI); Certificate Profiles; Part 1: Overview and common data structures",
                    href: "https://www.etsi.org/deliver/etsi_en/319400_319499/31941201/01.04.02_20/en_31941201v010402a.pdf",
                    date: "2020-07",
                    publisher: "ETSI",
                },
                "ETSI-LEGALPERSON": {
                    title: "ETSI EN 319 412-3 V1.2.1 (2020-07) - Electronic Signatures and Infrastructures (ESI); Certificate Profiles; Part 3: Certificate profile for certificates issued to legal persons",
                    href: "https://www.etsi.org/deliver/etsi_en/319400_319499/31941203/01.02.01_60/en_31941203v010201p.pdf",
                    date: "2020-07",
                    publisher: "ETSI",
                },
                "ETSI-JADES": {
                    title: "ETSI TS 119 182-1 V1.1.1 (2021-03) - Electronic Signatures and Infrastructures (ESI); JAdES digital signatures; Part 1: Building blocks and JAdES baseline signatures",
                    href: "https://www.etsi.org/deliver/etsi_ts/119100_119199/11918201/01.01.01_60/ts_11918201v010101p.pdf",
                    date: "2021-03",
                    publisher: "ETSI",
                },
                "DEP-DSS": {
                    title: "Algorithm for Validation of qualified and advanced signatures and seals",
                    href: "https://ec.europa.eu/digital-building-blocks/wikis/display/DIGITAL/Qualified+electronic+signature+-+QES+validation+algorithm",
                    date: "2019-10",
                    publisher: "European Commission",
                },
                "DID-PRIMER": {
                    title: "DID Primer",
                    href: "https://github.com/WebOfTrustInfo/rebooting-the-web-of-trust-fall2017/blob/master/topics-and-advance-readings/did-primer.md",
                    authors: ["Drummond Reed", "Manu Sporny"],
                    publisher: "Rebooting the Web of Trust 2017",
                },
                "DID-DNS": {
                    title: "The Decentralized Identifier (DID) in the DNS",
                    href: "https://tools.ietf.org/html/draft-mayrhofer-did-dns-01",
                    authors: ["A. Mayrhofer", "D. Klesev", "M. Sabadello"],
                    status: "Internet Draft",
                    publisher: "IETF",
                },
                "OWASP-TRANSPORT": {
                    title: "Transport Layer Protection Cheatsheet",
                    href: "https://www.owasp.org/index.php/Transport_Layer_Protection_Cheat_Sheet",
                },
            },
        };
    </script>
    <style>
        code {
            color: red;
        }
    </style>
</head>

<body>
    <p class="copyright">
        Copyright © 2023 the document editors/authors. Text is available under the
        <a rel="license" href="https://creativecommons.org/licenses/by/4.0/legalcode"
            >Creative Commons Attribution 4.0 International Public License</a
        >
    </p>
{{end}}
