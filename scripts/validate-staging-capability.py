#!/usr/bin/env python3
"""Check declared readback evidence; never mint an accepted staging receipt."""
import argparse
import json
from pathlib import Path


def string_list(value, name):
    if not isinstance(value, list) or any(not isinstance(x, str) or not x.strip() for x in value):
        raise ValueError(f'{name} must be a string array')
    return set(value)


def validate(capability, readback):
    routes = string_list(capability.get('required_routes'), 'required_routes')
    providers = string_list(capability.get('required_provider_dependencies'), 'required_provider_dependencies')
    if not routes:
        raise ValueError('required_routes must not be empty')
    observations = readback.get('observations')
    if not isinstance(observations, list):
        raise ValueError('observations must be an array')
    seen_routes, seen_providers, unrelated = set(), set(), []
    for item in observations:
        if not isinstance(item, dict):
            raise ValueError('observation must be an object')
        route, provider, status = item.get('route'), item.get('provider'), item.get('status')
        if not isinstance(route, str) or type(status) is not int or not 100 <= status <= 599:
            raise ValueError('observation must contain a route and HTTP status')
        required = route in routes or provider in providers
        if required:
            if not 200 <= status < 300 or item.get('business_verified') is not True:
                raise ValueError(f'required business readback failed: {route}')
            seen_routes.add(route)
            seen_providers.add(provider)
        elif status == 503 and item.get('error') == 'distribution_unavailable':
            # Known composition fallback only. Never infer configuration failure
            # from an arbitrary *_unavailable string or general server error.
            if provider != 'distribution' or not (route.startswith('/api/v1/distribution/') or route.startswith('/api/admin/distribution/') or route.startswith('/d/')):
                raise ValueError('distribution fallback has no matching route/provider')
            unrelated.append({'route': route, 'classification': 'external_config_unavailable'})
        elif not 200 <= status < 300:
            raise ValueError(f'unclassified failed observation: {route}')
    if routes - seen_routes or providers - seen_providers:
        raise ValueError('required route/provider readback is missing')
    return {'required_readback': 'passed', 'unrelated_observations': unrelated}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('capability', type=Path)
    parser.add_argument('readback', type=Path)
    args = parser.parse_args()
    try:
        result = validate(json.loads(args.capability.read_text()), json.loads(args.readback.read_text()))
    except (ValueError, TypeError, AttributeError) as exc:
        raise SystemExit(str(exc))
    print(json.dumps(result, sort_keys=True))


if __name__ == '__main__':
    main()
