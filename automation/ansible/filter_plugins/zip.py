# Ansible zip filter plugin
# Aggregates matching elements from lists or lists of dicts
#
# Marcin Hlybin, marcin.hlybin@gmail.com
#
from itertools import zip_longest

def zipped(list1, list2, longest=False, fillvalue=None):
    if all([type(x) is dict for x in list1]):
        if longest:
            list1 = fill_list_with_empty_dicts(list1, length=len(list2))

        for idx, item in enumerate(list1):
            try:
                item2 = list2[idx]
                if type(item2) is dict:
                    list1[idx].update(item2)
            except IndexError:
                break
        return list1
    else:
        if longest:
            return list(zip_longest(list1, list2, fillvalue=fillvalue))

        return list(zip(list1, list2))

def fill_list_with_empty_dicts(array, length):
    if len(array) < length:
        diff = length - len(array)
        for i in range(0, diff):
            array.append({})

    return array


class FilterModule(object):
    def filters(self):
        return {
            'zip': zipped
        }
