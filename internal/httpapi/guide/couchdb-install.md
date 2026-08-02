# CouchDB 설치 가이드

CouchHub는 CouchDB를 **직접 띄우지 않습니다.** 컨테이너 소켓을 마운트하면 사실상 호스트 root 권한을 노출하게 되므로, 기동은 compose가 맡고 CouchHub는 HTTP API로 설정만 프로비저닝합니다.

이미 운영 중인 CouchDB가 있다면 이 문서를 건너뛰고 바로 다음 단계(연결)로 가세요.

## 1. compose 파일

저장소의 `compose.yaml`과 `.env.example`을 그대로 쓰면 됩니다.

```sh
cp .env.example .env
# COUCHDB_PASSWORD, COUCHHUB_SECRET, SYNC_DOMAIN, HUB_DOMAIN 채우기
podman compose up -d     # 또는 docker compose up -d
```

`.env`에서 채울 값:

```sh
COUCHDB_USER=admin
COUCHDB_PASSWORD=          # openssl rand -base64 24
COUCHHUB_SECRET=           # openssl rand -base64 32
SYNC_DOMAIN=sync.example.com
HUB_DOMAIN=hub.example.com
```

`COUCHHUB_SECRET`은 로컬 저장소의 자격증명을 봉인하는 키입니다. **분실하거나 바꾸면 저장된 Setup URI와 존 토큰을 다시 읽을 수 없고**, Vault를 새로 발급해야 합니다.

CouchDB 포트는 호스트로 열지 않습니다 — Caddy만 통과합니다.

## 2. 주소 두 개를 구분하세요

CouchHub는 서로 다른 두 주소를 사용합니다. 이걸 같은 값으로 넣으면 **데스크톱에서는 동기화되는데 휴대폰에서는 안 되는** 상황이 됩니다.

| 항목 | 용도 | 예시 |
|---|---|---|
| CouchHub 연동용 | CouchHub → CouchDB (컨테이너 내부망) | `http://couchdb:5984` |
| Obsidian 연동용 | Obsidian → CouchDB (리버스 프록시 경유) | `https://sync.example.com` |

Obsidian 연동용 주소가 Setup URI에 들어갑니다. 휴대폰에서 접근 가능한 주소여야 합니다.

## 3. 리버스 프록시 (Caddy)

CouchDB는 **경로 루트에 마운트**해야 합니다. 서브패스(`/couchdb/`)는 지원되지 않습니다.

저장소의 `caddy/Caddyfile`이 이미 이렇게 되어 있습니다:

```caddyfile
{$SYNC_DOMAIN} {
	reverse_proxy couchdb:5984 {
		# nginx의 proxy_buffering off 에 해당합니다.
		# 버퍼링이 켜져 있으면 긴 _changes 롱폴링이 멈춘 것처럼 보입니다.
		flush_interval -1
	}
}

{$HUB_DOMAIN} {
	reverse_proxy couchhub:10020
}
```

프록시가 CORS 헤더를 **추가하면 안 됩니다.** CouchDB가 이미 보내므로 중복되고, `Access-Control-Allow-Origin`이 중복되면 브라우저가 요청을 거부합니다.

Caddy는 요청 본문 크기 제한이 기본적으로 없으므로 nginx의 `client_max_body_size`에 해당하는 설정은 필요 없습니다. 굳이 제한을 걸 경우 CouchDB의 `max_http_request_size`(4GiB)보다 크게 잡으세요.

## 4. CouchHub에서 연결

`http://<호스트>:10020` 접속 → 설치 마법사에서 위 두 주소와 관리자 계정을 입력하면, CouchHub가 현재 설정을 읽어 **바꿀 항목을 먼저 보여준 뒤** 적용합니다.

적용되는 내용:

- livesync 필수 설정 (CORS, `require_valid_user`, 문서·요청 크기 상한 등)
- `require_valid_user_except_for_up` — 헬스체크 경로 `/_up`만 인증 면제
- 시스템 데이터베이스 `_users`, `_replicator`, `_global_changes` 생성
- 단일 노드 클러스터 설정

## 문제 해결

**`_users database does not exist` 로그가 반복됨**
새로 만든 CouchDB의 정상 상태입니다. 설치 마법사를 완료하면 사라집니다.

**Obsidian에서 CORS 오류**
`cors.origins`에 `app://obsidian.md,capacitor://localhost,http://localhost`가 들어 있는지 확인하세요. 리버스 프록시가 CORS 헤더를 **덧붙이면** 중복되어 오히려 실패합니다. CORS는 CouchDB가 처리하게 두세요.

**동기화가 중간에 멈춤**
프록시 버퍼링이 켜져 있을 때 나타납니다. Caddy는 `flush_interval -1`, nginx는 `proxy_buffering off`.

**헬스체크가 401을 받아 컨테이너가 unhealthy로 표시됨**
`require_valid_user`를 켜면 `/_up`도 인증을 요구합니다. `chttpd.require_valid_user_except_for_up`이 `true`인지 확인하세요. 설치 마법사가 적용하는 항목이지만, 마법사 이전에 수동으로 `require_valid_user`만 켠 경우 빠져 있을 수 있습니다.

**휴대폰에서만 연결 실패**
Obsidian 연동용 주소가 내부망 주소로 들어갔을 가능성이 높습니다. 설정 화면에서 확인하세요.
