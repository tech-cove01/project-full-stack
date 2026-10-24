"""Stateful smoke check for the local dev environment, using only the stdlib."""
import argparse
import json
import secrets
import time
from urllib.error import HTTPError
from urllib.parse import urlencode
from urllib.request import Request, urlopen


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--base-url', default='http://127.0.0.1:8081')
    args = parser.parse_args()
    base = args.base_url.rstrip('/')

    def request(method, path, data=None, token=None):
        headers = {'Content-Type': 'application/json'}
        if token:
            headers['authorization'] = token
        payload = json.dumps(data).encode() if data is not None else None
        with urlopen(Request(base + path, data=payload, headers=headers, method=method), timeout=10) as response:
            return json.load(response)

    def api(method, path, data=None, token=None):
        result = request(method, path, data, token)
        if result.get('success') is not True:
            raise RuntimeError(result.get('errorMsg', 'API request failed'))
        return result.get('data')

    def login(phone):
        result = api('POST', '/user/code?' + urlencode({'phone': phone}))
        if not isinstance(result, dict) or result.get('mode') != 'development':
            raise RuntimeError('This smoke check requires the local dev profile.')
        return api('POST', '/user/login', {'phone': phone, 'code': result['code']})

    shops = api('GET', '/shop/of/name?current=1')
    assert shops, 'Demo shop list is empty; check SQL initialization.'

    owner = login('13800000000')
    owner_workspace = api('GET', '/merchant/workspace', token=owner)
    assert {shop['id'] for shop in owner_workspace['merchants'][0]['shops']} == {'1', '2'}

    staff = login('13900000000')
    staff_workspace = api('GET', '/merchant/workspace', token=staff)
    assert {shop['id'] for shop in staff_workspace['merchants'][0]['shops']} == {'1'}

    user = login('158' + f'{secrets.randbelow(100_000_000):08d}')
    order_id = api('POST', '/voucher-order/seckill/1001', token=user)
    assert isinstance(order_id, str), 'Order IDs must remain strings.'
    deadline = time.monotonic() + 20
    state = None
    while time.monotonic() < deadline:
        result = api('GET', '/voucher-order/' + order_id, token=user)
        state = result['state']
        if state != 'PROCESSING':
            break
        time.sleep(0.5)
    assert state == 'CREATED', f'Order did not finish creating: {state}'

    forbidden = request('GET', '/voucher-order/' + order_id, token=staff)
    assert forbidden.get('success') is False, 'Cross-user order query unexpectedly succeeded.'
    try:
        request('GET', '/merchant/workspace')
        raise AssertionError('Anonymous merchant query unexpectedly succeeded.')
    except HTTPError as error:
        assert error.code == 401

    print('PASS: shops, login, owner/staff shop scope, async order, cross-user and anonymous access checks.')


if __name__ == '__main__':
    main()
