"""Private stdio transport for protocol login; never persist or log request data."""
import base64
import json
import sys
from urllib.parse import urlsplit

from curl_cffi import requests

UA = 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/136.0.7103.113 Safari/537.36'
session = None

# The web callback host is kept as a wire descriptor so the public client does
# not advertise the upstream/product relationship.  The authenticated peer is
# still visible at runtime; this is branding hygiene, not encryption.
_BINDING_HOST = base64.b64decode('YXBwLmtpbG8uYWk=').decode('ascii')


def destination(url):
    parsed = urlsplit(url)
    remote = parsed.scheme == 'https' and parsed.hostname in ('auth.openai.com', 'sentinel.openai.com', _BINDING_HOST) and parsed.port in (None, 443)
    if not remote or parsed.username or parsed.password:
        raise ValueError('unexpected_destination')
    return parsed


def handle(value):
    global session
    kind = value.get('kind')
    if kind == 'init':
        if session is not None:
            raise ValueError('already_initialized')
        proxy = value.get('proxy') or ''
        if proxy and urlsplit(proxy).scheme not in ('http', 'https', 'socks5', 'socks5h'):
            raise ValueError('invalid_proxy')
        session = requests.Session(impersonate='chrome136', trust_env=False)
        if proxy:
            session.proxies = {'http': proxy, 'https': proxy}
        session.headers.update({'User-Agent': UA, 'Accept-Language': 'en-US,en;q=0.9',
                                'sec-ch-ua': '"Chromium";v="136", "Google Chrome";v="136", "Not.A/Brand";v="99"',
                                'sec-ch-ua-mobile': '?0', 'sec-ch-ua-platform': '"Windows"'})
        return {'userAgent': UA}
    if session is None:
        raise ValueError('not_initialized')
    if kind == 'cookies':
        parsed = destination(value['url'])
        return [{'name': c.name, 'value': c.value} for c in session.cookies.jar
                if parsed.hostname == c.domain.lstrip('.') or parsed.hostname.endswith('.'+c.domain.lstrip('.'))]
    if kind == 'request':
        url = value['url']
        parsed = destination(url)
        options = value.get('options') or {}
        method = options.get('method', 'GET')
        if method not in ('GET', 'POST') or (parsed.scheme == 'http' and method != 'GET'):
            raise ValueError('invalid_method')
        response = session.request(method, url, headers=options.get('headers') or {},
                                   data=options.get('body'), allow_redirects=False, timeout=35)
        if len(response.content) > 4 * 1024 * 1024:
            raise ValueError('response_too_large')
        return {'status': response.status_code, 'location': response.headers.get('location'),
                'headers': dict(response.headers), 'text': response.text}
    raise ValueError('invalid_operation')


def main():
    # Inputs contain credentials; communicate only via the owned process pipes.
    for line in sys.stdin.buffer:
        identity = None
        try:
            if len(line) > 1024 * 1024:
                raise ValueError('request_too_large')
            value = json.loads(line)
            identity = value.get('id')
            result = {'id': identity, 'ok': True, 'data': handle(value)}
        except Exception as error:
            code = getattr(error, 'code', 0)
            category = 'network_timeout' if code == 28 else 'proxy_connection_error' if code in (5, 7, 97) else 'protocol_transport_error'
            result = {'id': identity, 'ok': False, 'error': category}
        sys.stdout.write(json.dumps(result, ensure_ascii=True, separators=(',', ':'))+'\n')
        sys.stdout.flush()
    if session is not None:
        session.close()


if __name__ == '__main__':
    main()
