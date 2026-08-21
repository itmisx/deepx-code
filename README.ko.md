<div align="center">

# deepx-code

**DeepSeek 네이티브, OpenAI 호환 터미널 코딩 에이전트 —— 단일 바이너리, 캐시 친화적, 코드 그래프와 로컬 OCR 내장**

**DeepSeek · Xiaomi MiMo · Kimi · Qwen 프리셋 제공, 모든 커스텀 OpenAI 호환 모델 지원**

[![Go](https://img.shields.io/badge/built%20with-Go-00ADD8?logo=go&logoColor=white)](https://go.dev) [![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE) [![Release](https://img.shields.io/github/v/release/itmisx/deepx-code?color=success)](https://github.com/itmisx/deepx-code/releases) [![Downloads](https://img.shields.io/github/downloads/itmisx/deepx-code/total?color=success&label=downloads)](https://github.com/itmisx/deepx-code/releases) [![Stars](https://img.shields.io/github/stars/itmisx/deepx-code?style=flat)](https://github.com/itmisx/deepx-code/stargazers) ![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey)

[简体中文](README.md) · [English](README.en.md) · [日本語](README.ja.md) · **한국어**

![deepx-code demo](assets/demo.gif)

</div>

> [!TIP]
> **⚡ 긴 세션에서 prompt-cache 적중률 ~99% 실측**（실제 세션: 41,591 토큰 중 41,472 적중）. DeepSeek은 캐시 적중 입력을 캐시 미스의 수십분의 1로 과금하므로（[공식 가격](https://api-docs.deepseek.com/quick_start/pricing)）, 긴 실행에서도 반복되는 컨텍스트에 거의 과금되지 않습니다.

---

## ✨ 핵심 기능

- **🦫 단일 Go 바이너리** —— Node / Python 런타임 불필요, `curl` 한 줄 설치, macOS / Linux / Windows 지원.
- **💰 캐시 친화적, 긴 세션도 저렴** —— DeepSeek 프리픽스 캐시를 중심으로 설계(실측 ~99% 적중). 로컬 의미 라우팅은 매 턴 지연 0·토큰 0으로 시작.
- **🧭 코드 그래프 내장(codegraph)** —— 심볼 단위 정의 이동 / 호출자 / 인터페이스 구현 / 영향 범위. Go는 `go/types`로 정확히 해석하여 저장소 전체 grep을 대체.
- **👀 로컬 이미지 OCR(PaddleOCR)** —— 스크린샷의 텍스트를 오프라인으로 읽고, 멀티모달 API 불필요.
- **📎 `@` 파일/디렉터리 참조** —— 입력창에 `@` 입력 시 로컬 퍼지 경로 선택기가 열림. 선택하면 `@경로`가 메시지에 삽입되고 모델이 필요에 따라 Read(파일)/ List(디렉터리)를 호출. 정확한 컨텍스트 제공, 전부 욱여넣을 필요 없음.
- **🧠 듀얼 모델 자동 라우팅** —— 가벼운 작업은 flash, 복잡한 작업은 자동으로 pro 승격. `/model flash|pro`로 모델 고정, `/auto` `/plan` `/review`로 모드 전환도 가능.
- **🗂️ 순차 Todo + 병렬 Plan DAG** —— 다단계 작업은 보이는 체크리스트로 한 단계씩. 독립적인 병렬 작업은 DAG로 분해해 서브 에이전트를 병렬 실행.
- **🔁 재사용 가능한 Workflow** —— 반복 실행하는 멀티 에이전트 흐름을 JS 스크립트로 고정(`agent()` / `parallel()` / `pipeline()`): 다관점 리뷰, 팬아웃 리서치, 파이프라인, 빌 때까지 루프 등. `/ultracode <설명>`으로 모델이 자동 생성·저장, `/workflow <이름>`으로 실행. 진짜 병렬·중단 후 resume·구조화 출력은 도구로 강제·실행 전 모든 단계 표시 및 소요 시간 실시간 표시. Claude Code 의 workflow 스크립트 규약과 호환되어 스크립트를 그대로 상호 사용 가능.
- **💾 무손실 세션 영속화** —— gob가 `tool_calls` / 도구 결과 / `reasoning_content`를 완전 보존해 재시작 후에도 매끄럽게 이어감. 윈도가 차면 자동 계층 압축.
- **🔌 MCP + Skill 생태계** —— MCP 네이티브 지원. Claude의 skill 디렉터리와 호환되어 기존 skill을 그대로 재사용.
- **🛡️ 검토 모드** —— 파일 쓰기 / Shell 실행은 기본적으로 사람의 확인을 요구.
- **🧱 네이티브 OS 수준 샌드박스** —— 기본 `native`는 OS 격리: macOS Seatbelt, Linux bubblewrap — 쓰기를 workspace로 제한 + 프로세스 격리; OS 메커니즘이 없는 플랫폼은 소프트 정책 블랙리스트로 폴백. `docker` 컨테이너 격리나 `off`도 지원. 컨테이너 없이도 agent에 안전 경계를 그어준다.
- **🎛️ 작업 모드(working mode)** —— 명령 하나로 방법론을 고정: `karpathy`(실용주의) / `openspec`(스펙 주도) / `superpowers`(전체 워크플로 엄격). 세 모드는 상호 배타적 — 하나를 선택하면 나머지 두 개의 skill을 비활성화해 방법론 혼용을 방지. 세션에 저장되며 매 턴 히스토리를 오염시키지 않고 프롬프트 주입.
- **⚡ 비대화형 `exec` 모드** —— `deepx exec "작업"` 은 한 번 실행하고 결과를 바로 stdout으로 출력. 파이프로 입력하고, 출력을 리다이렉트하고, 스크립트 / CI / cron에 넣을 수 있어 **TUI에 들어갈 필요 없음**(아래 참조).

## 📊 Claude Code 비교

|                     | **deepx-code**                          | Claude Code              |
| :------------------ | :-------------------------------------- | :----------------------- |
| 배포                | 단일 Go 바이너리, `curl` 한 줄           | Node(npm)                |
| 오픈소스            | ✅ MIT                                  | ❌ 비공개                |
| 모델                | DeepSeek / Xiaomi MiMo(OpenAI 호환, 설정 시 공급자 선택, flash/pro 자동 라우팅) | Anthropic Claude        |
| 비용                | 긴 세션 ~99% 캐시 적중                   | 구독 / Claude API 사용량  |
| 코드 그래프 내장    | ✅ codegraph(Go는 `go/types`로 정확)     | ❌(grep / 검색)          |
| 로컬·오프라인 OCR   | ✅ PaddleOCR                            | ❌(이미지는 클라우드 멀티모달) |
| MCP                 | ✅                                      | ✅                       |
| Skill 생태계        | ✅(Claude skill 디렉터리 재사용)         | ✅                       |

> [!NOTE]
> 이 표는 모델 품질 자체를 비교하지 않습니다. deepx-code의 선택은 **비용·오픈소스·단일 바이너리·내장 코드 그래프·오프라인 OCR** 입니다.

## 🚀 빠른 시작

**1. 설치**

macOS / Linux(끝의 `&& exec $SHELL`이 현재 셸을 새로 시작해 PATH에 deepx를 즉시 반영합니다. rc 소스나 새 터미널을 열 필요가 없습니다):

```bash
curl -fsSL https://raw.githubusercontent.com/itmisx/deepx-code/main/scripts/install.sh | bash && exec $SHELL
```

Windows(PowerShell):

```powershell
irm https://raw.githubusercontent.com/itmisx/deepx-code/main/scripts/install.ps1 | iex
```

🇨🇳 중국 본토 사용자는 **Gitee 미러**로 더 빠르게 설치할 수 있습니다(소스·바이너리 모두 Gitee에서. 이후 `deepx upgrade`도 Gitee 사용):

macOS / Linux:

```bash
curl -fsSL https://gitee.com/itmisx/deepx-code/raw/main/scripts/install.sh | SOURCE=gitee bash && exec $SHELL
```

Windows PowerShell

```powershell
$env:SOURCE='gitee'; irm https://gitee.com/itmisx/deepx-code/raw/main/scripts/install.ps1 | iex
```

`~/.local/bin/deepx`에 설치됩니다. `deepx upgrade`로 언제든 업데이트.

**2. 터미널에서 프로젝트로 이동해 실행**

deepx는 **터미널 프로그램**입니다. 터미널을 열고 프로젝트로 `cd` 한 뒤 `deepx` 를 실행하면 대화형 UI로 들어갑니다.

- 어떤 터미널이든 OK: macOS Terminal / iTerm2, Linux 터미널, Windows Terminal / PowerShell.
- **VS Code 내장 터미널**도 추천(`Terminal → New Terminal`, 또는 `` Ctrl+` ``): 열려 있는 프로젝트 디렉터리에 이미 있어서 `deepx` 가 그 프로젝트를 대상으로 동작하고, 수정 사항이 에디터에 즉시 반영됩니다.

```bash
cd <당신의 프로젝트>   # VS Code 내장 터미널이면 보통 이미 프로젝트 루트에 있음
deepx                   # 대화형 TUI 진입
```

**3. 설정**

| 항목          | 방법                                                         |
| :------------ | :----------------------------------------------------------- |
| 공급자 & Key | 첫 실행 마법사에서 **←/→로 공급자(DeepSeek / Xiaomi MiMo)를 선택하고 해당 API Key를 입력**해 `~/.deepx/model.yaml`에 저장. 각 공급자에 flash/pro 기본 모델과 1M 컨텍스트 제공(DeepSeek `deepseek-v4-flash` / `-pro`, MiMo `mimo-v2.5` / `-pro`). `/config`로 재설정. |
| 수동 재정의   | `~/.deepx/model.yaml`을 직접 편집해 role(flash/pro)별로 `base_url` / `model` / `api_key` / `max_tokens` / `context_window`를 재정의 가능. flash와 pro가 서로 다른 공급자를 가리킬 수도 있음. |
| 다중 공급자 전환 | `/config` 할 때마다 설정이 공급자 이름(deepseek/mimo/kimi/qwen/custom)으로 `~/.deepx/provider.yaml`에 보관됨. `/provider`로 설정된 공급자 간 원탭 전환(해당 공급자의 flash/pro를 `model.yaml`에 다시 기록), Key 재입력 불필요. |
| Skill         | `<워크스페이스>/.deepx/skills/`에 두거나 `~/.claude/skills/` 등 재사용. |
| MCP           | TUI에서 `/mcp-add`로 추가, `/mcp-list`로 목록 확인.          |

## ⚡ 비대화형 실행（`deepx exec`）

전체 TUI에 들어가지 않고 deepx를 스크립트에 넣고 싶을 때는 `deepx exec "<작업>"` 을 사용하세요. 작업을 실행하고 결과를 그대로 터미널(stdout)에 출력한 뒤 종료합니다. 결과만 나오고 중간 과정은 표시되지 않습니다.

```bash
deepx exec "README의 기능 목록을 영어로 번역해 README.en.md에 작성"
```

파이프 입력도 지원합니다(`cat error.log | deepx exec "이 에러를 분석해줘"`). 먼저 대화형 `deepx`로 API 키를 설정해 두세요.

## 🧠 동작 원리

<details>
<summary><b>세션 영속화(gob 바이너리, 무손실 복원)</b></summary>

```
~/.deepx/sessions/<sha1(workspace)[:16]>/
├── meta.json          # 워크스페이스 메타 정보
├── state.json         # 압축 상태 + 사용량 스냅샷
├── YYYY-MM-DD.jsonl   # 텍스트 로그(Memory 검색용)
└── history.gob        # 완전한 바이너리 히스토리
```

| 형식               | 저장 내용                                                               | 용도                          |
| :----------------- | :--------------------------------------------------------------------- | :---------------------------- |
| `history.gob`      | system + user + assistant(`tool_calls`·도구 결과·`reasoning_content` 포함) | **재시작 복원, 매끄러운 이어가기** |
| `YYYY-MM-DD.jsonl` | user / assistant 일반 텍스트                                            | Memory 도구 검색              |

재시작 시 gob를 우선 로드하고 실패하면 JSONL로 폴백. 업그레이드나 skill 변경으로 system prompt가 바뀌면 gob 복원 시 현재 버전으로 투명하게 교체하여 캐시 프리픽스를 안정적으로 유지합니다.

</details>

<details>
<summary><b>세션 압축(계층 + 요약 병합)</b></summary>

컨텍스트 윈도의 70%를 넘으면 자동 발동: 꼬리 부분에 약 20K 토큰을 계층적으로 남기고, 오래된 내용은 LLM이 일관된 요약으로 압축해 기존 요약과 병합합니다. gob도 갱신되어 재시작 후에도 일관됩니다.

</details>

<details>
<summary><b>작업 계획: Todo(순차) vs Plan DAG(병렬)</b></summary>

- **Todo** —— 다단계·순차·컨텍스트 의존 작업(예: 앱을 처음부터 구축): 모델이 보이는 체크리스트에 단계를 나열하고 하나씩 체크하며 스스로 실행해, 실시간 진행 상황을 보여줍니다.
- **CreatePlan(Plan DAG)** —— 진짜로 병렬·독립적인 팬아웃: DAG로 분해해 의존 순서대로 병렬 서브 에이전트를 실행하고, 각 노드가 flash / pro를 선택한 뒤 집계합니다.

```
CreatePlan
  ├─ plan-1: Read  (flash) ─────┐
  ├─ plan-2: Read  (flash) ─────┤
  ├─ plan-3: Grep  (flash) ─────┤
  └─ plan-4: Write (pro)   ─────┘ depends_on: [1,2,3]
```

</details>

<details>
<summary><b>로컬 OCR(이미지 읽기 보완)</b></summary>

이미지를 붙여넣거나 경로를 주면 LLM이 `OCR` 도구(PaddleOCR PP-OCRv5)로 텍스트를 읽습니다. 첫 호출 시 OCR 모델(~37MB)과 ONNX runtime을 내려받고, 이후에는 **오프라인으로 수 초 내** 응답. 멀티모달 API 없이도 에러 스크린샷이나 UI 목업을 에이전트가 "읽을" 수 있습니다.

</details>

### 🚦 모델 라우팅(로컬 의미 판정, 지연 0, 토큰 0)

메시지가 도착하면 deepx는 **로컬에서** 이번 턴을 flash로 시작할지 pro로 시작할지 결정합니다. 추가 LLM 토큰은 전혀 쓰지 않습니다. 판정 기준은 두 가지뿐입니다:

```
메시지 > 500자                                       → pro(모델 불필요, 오프라인에서도 동작)
「pro로 승격」 그룹과의 최대 유사도 ≥ 0.91
        그리고 「flash 유지」 그룹보다 높을 것         → pro
그 외                                                → flash(폴백)
```

비교 대상은 키워드 포함 여부가 아니라 **문장 전체의 의미**입니다 —— "이 한 줄 작성 방식을 다듬어 줘"는 "최적화"라는 단어가 있어도 승격되지 않고, "디자인 시안은 어느 디렉터리야?"도 "디자인"에 걸리지 않습니다. 최초 실행 시 양자화된 multilingual-e5-small을 백그라운드로 내려받으며(118 MB, 소스는 ModelScope → hf-mirror → HuggingFace 순으로 시도) **시작을 막지 않습니다**. 준비되기 전이거나 다운로드가 실패하면 진입 라우팅을 아예 하지 않고 항상 flash로 시작합니다.

> `/model flash|pro`로 고정하면 라우팅을 우회하고, `auto`(기본값)일 때만 위 규칙이 동작합니다. 시작 모델은 해당 턴 동안 고정되며, 모델은 도중에 `SwitchModel`로 pro로 승격할 수 있지만 되돌아오지는 않습니다(모델을 바꾸면 프롬프트 캐시가 전부 무효화되기 때문).

<details>
<summary><b>라우팅 조정: 두 그룹의 예문과 여섯 개 명령</b></summary>

두 그룹은 대등하며, 각각 반대 방향의 오류를 고칩니다:

| 그룹              | 내장 | 의미                        | 언제 추가하나                                  |
| :---------------- | ---: | :-------------------------- | :--------------------------------------------- |
| **pro로 승격**    |   36 | 이런 문장 → pro로 시작      | **누락**: pro여야 하는데 flash로 시작됨        |
| **flash 유지**    |   22 | 이런 문장 → flash로 되돌림  | **오판**: 개념 질문이나 사소한 수정이 pro로 승격됨 |

> 「flash 유지」 그룹은 **이미 임계값을 넘었지만 실제로는 개념 질문이나 소소한 수정인 메시지를 되돌리는** 역할입니다. "이 그룹에 맞아야 flash가 된다"는 뜻이 아닙니다 —— 어느 그룹에도 맞지 않는 메시지는 원래 flash입니다.

| 명령                                                         | 용도                                             |
| :------------------------------------------------------------ | :----------------------------------------------- |
| `/router-list-pro` `/router-list-flash`                        | 그룹의 현재 예문(번호 포함)과 실제 규칙 보기     |
| `/router-add-pro <문장>`                                       | 누락이 났을 때                                   |
| `/router-add-flash <문장>`                                     | 오판이 났을 때                                   |
| `/router-delete-pro <번호>` `/router-delete-flash <번호>`      | 목록의 번호로 삭제(그룹마다 1번부터)             |

예문 작성 두 가지 원칙:

1. **키워드가 아니라 완전한 작업 문장으로 쓸 것.** ✅ `이 서비스의 카나리 배포 절차를 정리해 줘`; ❌ `카나리 배포 절차` —— 단어 나열은 벡터가 실제 메시지와 멀어 거의 아무것도 매칭되지 않습니다.
2. **구체적일수록 안전하다.** 지나치게 일반적인 예문은 주변의 사소한 요청까지 끌어옵니다(실측: 너무 넓은 예문 하나가 2건을 살리면서 2건을 망가뜨렸습니다).

`~/.deepx/router.yaml`을 직접 편집해도 됩니다(`patterns` = pro로 승격, `flash_patterns` = flash 유지). **다음 메시지부터 바로 적용되며 재시작이 필요 없습니다.** 이 파일은 최초 실행 시 두 그룹의 내장 기본값과 함께 자동 생성됩니다. 수정하지 않은 그룹은 deepx가 내장 표를 갱신할 때 자동 동기화되고, **수정한 그룹은 절대 덮어쓰지 않습니다**. 그룹을 비우거나 파일을 지우면 내장 기본값으로 돌아갑니다.

</details>

### 🧭 코드 그래프(codegraph)

심볼 그래프 엔진을 내장하여, 저장소 전체 grep과 파일을 하나씩 여는 대신 모델이 심볼 단위 내비게이션 + 호출 관계 쿼리를 직접 수행합니다.

<details>
<summary><b>op 치트시트(12개)</b></summary>

| op             | 용도                       | 필수                       | 설명                                            |
| :------------- | :------------------------- | :------------------------- | :---------------------------------------------- |
| `def`          | 심볼 정의 위치              | `name`                    | 함수 / 타입 / 메서드 / 변수의 정의 위치         |
| `refs`         | 심볼 사용처                 | `name`                    | 모든 참조(정의 + 호출 + 읽기)                   |
| `symbols`      | 이름으로 퍼지 검색          | `name`(선택), `kind`(선택) | `kind`: func/method/type/var/const/field        |
| `outline`      | 파일 내 심볼 목록           | `path`                    | 파일 아웃라인                                   |
| `imports`      | 파일의 import 목록          | `path`                    | 의존성 개요                                     |
| `callers`      | 함수의 호출자               | `name`                    | **변경 시 영향 범위**, Go 암시적 인터페이스도 포함 |
| `callees`      | 함수가 호출하는 것          | `name`                    | 내부 흐름 이해                                  |
| `implementers` | 인터페이스 구현자           | `name`                    | Go 암시적 인터페이스를 **심볼 정밀도**로. grep 불가 |
| `subtypes`     | 타입을 상속 / 임베드하는 것 | `name`                    | 서브타입 추적                                   |
| `supertypes`   | 타입의 파생 원본            | `name`                    | 슈퍼타입 / 임베드 인터페이스                    |
| `impact`       | 변경의 하위 영향            | `name`, `depth`(기본 3)   | 전이 폐포, 영향 범위 분석                       |
| `reindex`      | 인덱스 강제 재구축          | —                          | 캐시 이상 시 수동 트리거                        |

</details>

**지원 언어**: Go(stdlib 정밀 파싱) + TypeScript / JavaScript / Python / Java / Rust / C / C++ / C# / Ruby / PHP / Kotlin / Swift / Scala / Dart / Vue / Svelte.

**동작**: 시작 시 백그라운드 `Prewarm`이 인덱스를 구축(`loading → ready`). Write/Update로 변경된 파일은 `stale`로 표시되어 다음 쿼리에서 증분 재구축. 결과는 `파일:행`(시그니처 / 호출자 포함)으로 표시되고 페이지네이션됩니다.

## 🧰 도구

| 종류        | 도구                               |       plan | auto | review |
| :---------- | :--------------------------------- | ---------: | :--: | :----: |
| 읽기 전용   | `Read` `List` `Tree` `Glob` `Grep` |          ✓ |  ✓   |   ✓    |
| 코드 그래프 | `CodeGraph`                        |          ✓ |  ✓   |   ✓    |
| 파일 쓰기   | `Write` `Update`                   |          ✗ |  ✓   |   ⏳   |
| Shell       | `Bash`                             |          ✗ |  ✓   |   ⏳   |
| 웹          | `Search` `Fetch`                   |          ✓ |  ✓   |   ✓    |
| 메모리      | `Memory`                           |          ✓ |  ✓   |   ✓    |
| Skill       | `LoadSkill`                        |          ✓ |  ✓   |   ✓    |
| 이미지      | `OCR`                              |          ✓ |  ✓   |   ✓    |
| 계획        | `Todo` `CreatePlan`                | LLM이 호출 |     |        |
| 승격        | `SwitchModel`                      | LLM이 호출 |     |        |

> ⏳ = 자동 실행되지만 사람의 확인이 필요.

## ⌨️ 슬래시 명령

| 명령                                 | 동작                                |
| :----------------------------------- | :---------------------------------- |
| `/plan` `/auto` `/review`            | 모드 전환(읽기 전용 / 자동 / 검토)  |
| `/model`                             | 모델 선택 팝업(auto=작업별 라우팅 / flash / pro 고정); `/model flash` 직접 지정도 가능 |
| `/provider`                          | 설정된 공급자 간 빠른 전환: 팝업에서 선택(`/provider <이름>` 직접 지정도 가능). `/config` 할 때마다 설정이 공급자 이름으로 `~/.deepx/provider.yaml`에 보관되며, 전환 시 해당 공급자의 flash/pro를 `model.yaml`에 다시 기록 |
| `/reasoning`                         | role(flash/pro)별로 `thinking` / `reasoning_effort` 설정 팝업; 빈 값 = 해당 필드 미전송(MiMo 등 미지원 모델에 영향 없음) |
| `/router-list-pro` `/router-list-flash` `/router-add-pro` `/router-add-flash` `/router-delete-pro` `/router-delete-flash` | 라우터의 두 예문 그룹 조정(「🚦 모델 라우팅」 참고): `list`는 번호 목록과 실제 규칙 표시, `add <문장>` 추가, `delete <번호>` 삭제. 누락은 `-pro` 그룹에, 오판(개념 질문 / 사소한 수정)은 `-flash` 그룹에. `~/.deepx/router.yaml` 직접 편집도 가능하며 다음 메시지부터 적용 |
| `/compact`                           | 세션 수동 압축                      |
| `/new` `/sessions`                   | 새 대화 시작 / 기록 목록(↑↓ 선택, Enter 전환) |
| `/status`                            | 오른쪽 상태 패널 표시/숨김(`Ctrl+B` 도 가능) |
| `/web-config`                        | 웹 대시보드 바인드 IP·포트를 팝업으로 설정("IP [포트]"를 공백으로 구분 입력; IP 비움/`127.0.0.1`=로컬 전용, `0.0.0.0`=LAN에서 휴대폰/태블릿 접속 가능, 포트 생략=랜덤). 저장 즉시 재시작 없이 적용되고 새 주소를 표시; 설정은 세션의 `meta.json`에 저장되며 접근 토큰은 세션별로 고정되어 재시작 후에도 동일. ⚠️ 이 패널은 세션을 제어하고 명령을 실행할 수 있으며 평문 HTTP이므로 신뢰할 수 있는 LAN에서만 노출하세요 |
| `/sandbox`                           | 샌드박스: `off`(끄기) / `native`(기본, OS 격리: macOS Seatbelt, Linux bubblewrap — 쓰기를 workspace로 제한 + 프로세스 격리; OS 메커니즘이 없는 플랫폼은 소프트 정책 블랙리스트로 폴백) / `docker`(컨테이너 격리, `/sandbox docker <image>`) | / `native`(기본, OS 격리: macOS Seatbelt, Linux bubblewrap — 쓰기를 workspace로 제한 + 프로세스 격리; OS 메커니즘이 없는 플랫폼은 소프트 정책 블랙리스트로 폴백) / `docker`(컨테이너 격리, `/sandbox docker <image>`) |
| `/working-mode`                      | 작업 모드(방법론): `karpathy`(기본, 실용주의) / `openspec`(스펙 주도) / `superpowers`(전체 워크플로 엄격); 팝업으로 선택하거나 `/working-mode kp\|spec\|sp`로 직접 전환. 세 모드는 상호 배타적 — 하나를 선택하면 나머지 두 개의 skill을 비활성화해 방법론 혼용을 방지. 세션에 저장되며 매 턴 히스토리를 오염시키지 않고 프롬프트 주입 |
| `/ultracode` `/workflow` `/workflows` | Workflow(JS 멀티 에이전트 편성): `/ultracode <설명>`으로 모델이 생성·저장, `/workflow <이름> [k=v]`으로 실행(실행 전 확인), `/workflows`로 목록 |
| `/lang`                              | UI 언어 전환(중 / 영)               |
| `/mcp-list` `/mcp-add` `/mcp-delete` | MCP 서버 관리                       |
| `/skills` `/config` `/mode`          | skill 목록 / key 재설정 / 모드 표시 |
| `/help`                              | 도움말                              |
| `/exit`                              | deepx 종료                          |

## 🛡️ 검토 모드

| 모드               | Write / Update / Bash | 기타 도구 | 명령      |
| :----------------- | :-------------------- | :-------- | :-------- |
| `review`(기본)     | 사람이 YES/NO 확인    | 자동 실행 | `/review` |
| `auto`             | 자동 실행             | 자동 실행 | `/auto`   |
| `plan`             | 비활성                | 자동 실행 | `/plan`   |

## 📦 Skill

```
워크스페이스  <wd>/.deepx/skills/
글로벌        ~/.agents/skills/ → ~/.claude/skills/ → ~/.deepx/skills/
```

- 워크스페이스 단위는 `git add`로 팀과 공유 가능
- 글로벌은 Claude Code 호환 —— 기존 skill을 그대로 재사용

## 🏗️ 아키텍처

<details>
<summary><b>데이터 흐름 펼치기</b></summary>

```
1 턴:
  사용자 입력
    ↓
  RouteByKeyword (로컬) ─► flash 또는 pro
    ↓
  StartStream (메인 루프)
    ├─ 직접 답변
    ├─ 도구 호출 → review가 쓰기/Shell 게이트 → 실행 → 결과 회신 → 계속
    ├─ Todo → 보이는 체크리스트(메인 에이전트가 단계별 실행)
    ├─ SwitchModel → pro 승격
    └─ CreatePlan → DAG scheduler → 병렬 서브 에이전트 → 집계

영속화:
  HistoryUpdateMsg → SaveGob (history.gob, 완전 충실)
  StreamDoneMsg    → Append JSONL (일반 텍스트, Memory 검색)
  재시작           → LoadGob (우선) / JSONL (폴백)

압축:
  tokens ≥ ctxWindow × 70% → runCompression (비동기)
    → 꼬리에 ~20K 토큰 유지 → LLM이 신·구 요약 병합 → gob + state.json 갱신
```

</details>

**디렉터리 구조**

```
deepx/
├── main.go
├── agent/      StartStream 도구 루프 + 라우팅 + DAG 스케줄러 + 서브 에이전트
├── config/     ~/.deepx/model.yaml 읽기/쓰기
├── session/    gob 영속화 + JSONL 로그 + 압축 상태
├── tools/      모든 도구 구현(읽기/쓰기 / 검색 / OCR / Memory / Skill / Plan / CodeGraph)
├── codegraph/  코드 그래프: 정의 / 호출 / 상속 구현 / 영향 범위
├── skill/      다중 경로 skill 탐색 및 로드
├── ocr/        PaddleOCR 래퍼(ONNX Runtime)
├── tui/        bubbletea TUI(입력 / 렌더 / 클립보드 / 선택 / 대시보드)
└── scripts/    설치 스크립트
```

## 💰 토큰 경제

- **라우팅 토큰 0**: 순수 로컬 문장 임베딩, LLM 호출 없음
- **도구 사전 주입 없음**: `Memory` / `LoadSkill`은 호출 시에만 context에 진입
- **system prompt 최소화**: 도구 공통 규약 + workspace만, 트리거 조건은 각 도구 description에
- **DeepSeek KV 캐시 친화**: tools 배열은 모드 / 역할로 바뀌지 않고, system prompt는 gob 복원 시 버전 인식
- **맹목적 검색보다 코드 그래프**: read / glob / grep 토큰 낭비를 근본부터 절감

## 🩹 제거

```bash
# macOS / Linux
rm -f ~/.local/bin/deepx && rm -rf ~/.deepx

# Windows: %LOCALAPPDATA%\Programs\deepx 와 %USERPROFILE%\.deepx 삭제
```

## 📄 License

[MIT](LICENSE) © 2026 itmisx
