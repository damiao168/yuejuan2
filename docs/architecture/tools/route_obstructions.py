"""依据原生渲染报告修正遮挡连线；只改变图纸路由，不改变依赖、状态或业务关系。"""
from pathlib import Path
import heapq
import json
import math

BASE = Path(__file__).resolve().parents[1]

def intersects(a, b, rect):
    x1,y1,x2,y2=rect
    if abs(a[0]-b[0])<0.01:
        return x1 < a[0] < x2 and min(a[1],b[1]) < y2 and max(a[1],b[1]) > y1
    if abs(a[1]-b[1])<0.01:
        return y1 < a[1] < y2 and min(a[0],b[0]) < x2 and max(a[0],b[0]) > x1
    return True

def ports(n):
    x,y,w,h=n['x'],n['y'],n['w'],n['h']
    return [((x+w+20,y+h*v),(1,v)) for v in (.5,.25,.75)] + [((x-20,y+h*v),(0,v)) for v in (.5,.25,.75)] + [((x+w*v,y+h+20),(v,1)) for v in (.5,.25,.75)] + [((x+w*v,y-20),(v,0)) for v in (.5,.25,.75)]

def usable_port(point, page, rectangles):
    inside_page = 20 <= point[0] <= page['width'] - 20 and 132 <= point[1] <= page['height'] - 80
    inside_node = any(left < point[0] < right and top < point[1] < bottom for left, top, right, bottom in rectangles)
    return inside_page and not inside_node


def routing_grid(page, nodes, starts, goals):
    xs = sorted({20, page['width'] - 20, *[node['x'] - 20 for node in nodes.values()],
                 *[node['x'] + node['w'] + 20 for node in nodes.values()], *[point[0] for point, _ in starts + goals]})
    ys = sorted({132, page['height'] - 80, *[node['y'] - 20 for node in nodes.values()],
                 *[node['y'] + node['h'] + 20 for node in nodes.values()], *[point[1] for point, _ in starts + goals]})
    return [x for x in xs if 20 <= x <= page['width'] - 20], [y for y in ys if 132 <= y <= page['height'] - 80]


def recovered_path(key, previous, xs, ys):
    path = []
    tail = key
    while tail:
        path.append((xs[tail[0]], ys[tail[1]]))
        tail = previous.get(tail)
    path.reverse()
    simplified = [path[0]]
    for index in range(1, len(path) - 1):
        first, middle, last = path[index - 1:index + 2]
        if not (first[0] == middle[0] == last[0] or first[1] == middle[1] == last[1]):
            simplified.append(middle)
    simplified.append(path[-1])
    return simplified


def add_neighbors(queue, key, cost, xs, ys, forbidden, rectangles, clear_cache, distances, previous, origins, count):
    i, j, direction = key
    for ni, nj, next_direction in ((i - 1, j, 0), (i + 1, j, 0), (i, j - 1, 1), (i, j + 1, 1)):
        if not 0 <= ni < len(xs) or not 0 <= nj < len(ys) or (ni, nj) in forbidden:
            continue
        first, second = (xs[i], ys[j]), (xs[ni], ys[nj])
        cache_key = tuple(sorted((first, second)))
        if cache_key not in clear_cache:
            clear_cache[cache_key] = not any(intersects(first, second, rectangle) for rectangle in rectangles)
        if not clear_cache[cache_key]:
            continue
        candidate = (ni, nj, next_direction)
        next_cost = cost + abs(first[0] - second[0]) + abs(first[1] - second[1]) + (30 if direction != next_direction else 0)
        if next_cost < distances.get(candidate, math.inf):
            distances[candidate] = next_cost
            previous[candidate] = key
            origins[candidate] = origins[key]
            heapq.heappush(queue, (next_cost, count, candidate))
            count += 1
    return count


def search_grid(starts, goals, xs, ys, forbidden, rectangles):
    goal_indices = {(xs.index(point[0]), ys.index(point[1])): side for point, side in goals}
    queue, distances, previous, origins = [], {}, {}, {}
    count = 0
    for point, side in starts:
        i, j = xs.index(point[0]), ys.index(point[1])
        key = (i, j, 0 if side[0] in (0, 1) else 1)
        penalty = 0 if side in ((1, .5), (.5, 1)) else 12
        distances[key], origins[key] = penalty, side
        heapq.heappush(queue, (penalty, count, key))
        count += 1
    clear_cache = {}
    # 同成本时保留原始邻居和端口顺序；重构不能改变稳定的布线路由选择。
    while queue:
        cost, _, key = heapq.heappop(queue)
        if cost != distances.get(key):
            continue
        if key[:2] in goal_indices:
            return origins[key], goal_indices[key[:2]], recovered_path(key, previous, xs, ys)
        count = add_neighbors(queue, key, cost, xs, ys, forbidden, rectangles, clear_cache,
                              distances, previous, origins, count)
    return None


def route(page, e):
    nodes = {node['id']: node for node in page['nodes'] if node.get('kind') != 'group'}
    source, target = nodes[e['source']], nodes[e['target']]
    rectangles = [(node['x'] - 9, node['y'] - 9, node['x'] + node['w'] + 9, node['y'] + node['h'] + 9) for node in nodes.values()]
    starts = [(point, side) for point, side in ports(source) if usable_port(point, page, rectangles)]
    goals = [(point, side) for point, side in ports(target) if usable_port(point, page, rectangles)]
    xs, ys = routing_grid(page, nodes, starts, goals)
    forbidden = {(i, j) for i, x in enumerate(xs) for j, y in enumerate(ys)
                 if any(left < x < right and top < y < bottom for left, top, right, bottom in rectangles)}
    result = search_grid(starts, goals, xs, ys, forbidden, rectangles)
    if result is None:
        raise ValueError(f'No route {e["source"]} -> {e["target"]}')
    return result

def main():
    report=json.loads((BASE/'evidence/native-render-validation.json').read_text(encoding='utf-8'))
    targets={}
    for p in report['results']:
        if p.get('obstructions'):
            targets[(p['file'],p['id'])]={int(o['edge'][3:]) for o in p['obstructions']}
    changes=[]
    for file in (BASE/'specs').glob('*.json'):
        spec=json.loads(file.read_text(encoding='utf-8-sig'))
        edited=False
        for page in spec['pages']:
            for idx in targets.get((spec['file'],page['id']),set()):
                e=page['edges'][idx]
                source,target,points=route(page,e)
                e.update(exitX=source[0],exitY=source[1],entryX=target[0],entryY=target[1],points=points)
                changes.append({'file':spec['file'],'page':page['id'],'edge':idx,'source':e['source'],'target':e['target']})
                edited=True
        if edited:
            file.write_text(json.dumps(spec,ensure_ascii=False,indent=2),encoding='utf-8')
    (BASE/'evidence/edge-routing-adjustments.json').write_text(json.dumps(changes,ensure_ascii=False,indent=2),encoding='utf-8')
    print(json.dumps({'adjusted_edges':len(changes)}))

if __name__=='__main__':
    main()
