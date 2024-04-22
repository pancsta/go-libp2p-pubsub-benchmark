import os
import importlib.util
from grafanalib.core import (
    Dashboard, TimeSeries, RowPanel, Target
)


def import_from_path(path):
    spec = importlib.util.spec_from_file_location("module.name", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


# include the shared am grafana funcs
am_grafana = import_from_path(os.getcwd()+"/am_grafana.py")


interval = '5s'
panels = []

# add sim panels
panels.extend([
    RowPanel(title="Simulator", gridPos=am_grafana.next_row_pos()),

    TimeSeries(
        title="Topics",
        gridPos=am_grafana.next_grid_pos(),
        dataSource='prometheus',
        interval=interval,
        targets=[

            Target(
                legendFormat="Avg peers per topic",
                expr='sim_peers_per_topic',
            ),
            Target(
                legendFormat="Topics",
                expr='sim_topics',
            ),
        ]),

    TimeSeries(
        title="Messages",
        gridPos=am_grafana.next_grid_pos(),
        dataSource='prometheus',
        interval=interval,
        targets=[

            Target(
                legendFormat="Delivered msgs",
                expr='sim_msgs_recv',
            ),
            Target(
                legendFormat="Missed msgs",
                expr='sim_msgs_miss',
            ),
        ]),

    TimeSeries(
        title="Peers",
        gridPos=am_grafana.next_grid_pos(),
        dataSource='prometheus',
        interval=interval,
        targets=[

            Target(
                legendFormat="Friendships",
                expr='sim_friends',
            ),
            Target(
                legendFormat="Peers",
                expr='sim_peers',
            ),
            Target(
                legendFormat="Peers with topics",
                expr='sim_peer_with_topics',
            ),
        ]),

    TimeSeries(
        title="Connections",
        gridPos=am_grafana.next_grid_pos(),
        dataSource='prometheus',
        interval=interval,
        targets=[

            Target(
                legendFormat="Connections",
                expr='sim_connections',
            ),
            Target(
                legendFormat="Streams",
                expr='sim_streams',
            ),
        ],
    ),
])

# loop over env var IDS, divided by comma
for pid in os.environ['GRAFANA_IDS'].split(','):
    # replace - with _
    pid = pid.replace('-', '_')

    # append to existing panels
    panels.extend(am_grafana.mach_panels(pid))

dashboard = Dashboard(
    title="libp2p-pubsub simulator",
    description="libp2p-pubsub simulator and it's state machines (" + os.environ['GRAFANA_IDS'] + ")",
    refresh='5s',
    # tags=[
    #     'example'
    # ],
    timezone="browser",
    panels=panels,
).auto_panel_ids()
