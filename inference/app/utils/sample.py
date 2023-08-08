import time
import GPUtil
import numpy as np


def choose_frames(frame_label_list, sample_fraction):
    samples = int(np.round(sample_fraction * len(frame_label_list)))
    indices = np.linspace(-1, len(frame_label_list) - 1, samples + 1, endpoint=True)[1:]
    indices = np.round(indices).astype(int)
    assert indices.size == samples, f"indices had {indices.size} values but samples is {samples}"
    frames_chosen = [frame_label_list[chosen_index][0] for chosen_index in indices]
    labels_chosen = [frame_label_list[chosen_index][1] for chosen_index in indices]
    return frames_chosen, labels_chosen


def get_gpu_utilization(time_period=30):
    gpus = GPUtil.getGPUs()
    if not gpus:
        print("No GPU available.")
        return None
    gpu = gpus[0]
    initial_load = gpu.load
    time.sleep(time_period)
    final_load = gpu.load
    average_load = (initial_load + final_load) / 2
    return average_load * 100
