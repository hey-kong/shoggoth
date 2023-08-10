#!/usr/bin/env python
# -*- coding: utf-8 -*-

import torch
import torch.nn as nn
from pytorchcv.models.resnet import ResBlock
from pytorchcv.model_provider import get_model


def remove_sequential(network, all_layers):
    for layer in network.children():
        if isinstance(layer, nn.Sequential):  # if sequential layer, apply recursively to layers in sequential layer
            remove_sequential(layer, all_layers)
        else:  # if leaf node, add it to list
            all_layers.append(layer)


def remove_ResBlock(cur_layers):
    all_layers = []
    for layer in cur_layers:
        if isinstance(layer, ResBlock):
            for ch in layer.children():
                all_layers.append(ch)
        else:
            all_layers.append(layer)
    return all_layers


class ResNet18(nn.Module):
    def __init__(self, latent_layer_num=7):
        super().__init__()

        model = get_model("resnet18", pretrained=True)
        model.features.final_pool = nn.AvgPool2d(4)

        all_layers = []
        remove_sequential(model, all_layers)
        all_layers = remove_ResBlock(all_layers)

        lat_list = []
        end_list = []

        for i, layer in enumerate(all_layers[:-1]):
            if i <= latent_layer_num:
                lat_list.append(layer)
            else:
                end_list.append(layer)

        self.lat_features = nn.Sequential(*lat_list)
        self.end_features = nn.Sequential(*end_list)

        self.output = nn.Linear(1024, 50, bias=False)

    def forward(self, x, latent_input=None, return_lat_acts=False):
        orig_acts = self.lat_features(x)
        if latent_input is not None:
            lat_acts = torch.cat((orig_acts, latent_input), 0)
        else:
            lat_acts = orig_acts

        x = self.end_features(lat_acts)
        x = x.view(x.size(0), -1)
        logits = self.output(x)

        if return_lat_acts:
            return logits, orig_acts
        else:
            return logits


if __name__ == "__main__":
    model = ResNet18()
    for name, param in model.named_parameters():
        print(name)
