---
stages:
  - planning
  - deployment

image: $DOCKER_IAC_TOOLS_IMAGE:latest

{{ range .Dirs }}
Plan {{ .SourcePath }}:
  stage: planning
  resource_group: {{ .SourcePath }}
  tags:
    - docker-{{ default "dev" $.Workload }}
  {{- if $.Needs }}
  needs: []
  {{- end }}
  script:
    - cd {{ .SourcePath }}
    - TF_INPUT=false terragrunt plan --out plan
  cache:
    policy: push
    key: {{ .SourcePath | replace "/" "-" }}
    paths:
      - {{ .SourcePath }}/.terragrunt-cache/
  rules:
    - changes:
      {{- range .Dependencies }}
        - {{ . -}}
      {{ end }}

Apply {{ .SourcePath }}:
  stage: deployment
  tags:
    - docker-{{ default "dev" $.Workload }}
  {{- if $.Needs }}
  needs: [Plan {{ .SourcePath }}]
  {{- end }}
  resource_group: {{ .SourcePath }}
  environment: {{ .SourcePath }}
  script:
    - cd {{ .SourcePath }}
    - ls .terragrunt-cache/
    - TF_INPUT=false terragrunt apply plan
  cache:
    policy: pull
    key: {{ .SourcePath | replace "/" "-" }}
    paths:
      - {{ .SourcePath }}/.terragrunt-cache/
  rules:
    - when: manual
      changes:
        {{- range .Dependencies }}
        - {{ . -}}
        {{ end }}
    {{/* In case you exceed the 50 dependencies, you can group them by directory */}}        
    {{/* {{- range .DependenciesGrouped }}
    - when: manual
      changes:
      {{- range .Items }}
        - {{ . }}
      {{- end }}
    {{ end }}    
{{ end }}